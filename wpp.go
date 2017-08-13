// A web pre-processor that assembles a single HTML file.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/0xABAD/filewatch"
	"github.com/gorilla/websocket"
)

var (
	OptOutfile    string
	OptHelp       bool
	OptVerbose    bool
	OptDevmode    bool
	OptDevport    uint
	OptTemplate   string
	OptIgnore     string
	ProgWebSocket *websocket.Conn
)

func init() {
	flag.BoolVar(&OptHelp, "help", false, UsageHelp)
	flag.BoolVar(&OptHelp, "h", false, UsageHelp)
	flag.BoolVar(&OptVerbose, "verbose", false, UsageVerbose)
	flag.BoolVar(&OptVerbose, "v", false, UsageVerbose)
	flag.StringVar(&OptOutfile, "outfile", "", UsageOutfile)
	flag.StringVar(&OptOutfile, "o", "", UsageOutfile)
	flag.StringVar(&OptTemplate, "template", "", UsageTemplate)
	flag.StringVar(&OptTemplate, "t", "", UsageTemplate)
	flag.StringVar(&OptIgnore, "ignore", "", UsageIgnore)
	flag.StringVar(&OptIgnore, "i", "", UsageIgnore)
	flag.BoolVar(&OptDevmode, "devmode", false, "enable the dev server for hot reloading")
	flag.UintVar(&OptDevport, "devport", 8082, "port to use with dev server")

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, UsageProgram)
		flag.PrintDefaults()
	}

	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

func main() {
	flag.Parse()
	if OptHelp {
		flag.Usage()
		os.Exit(0)
	}

	inputdir := flag.Arg(0)
	if inputdir == "" {
		flog("No input directory specified.  See wpp -help.")
	}

	var (
		err  error
		html string
		out  io.Writer
		file *os.File
		stat os.FileInfo
	)

	stat, err = os.Stat(inputdir)
	if os.IsNotExist(err) {
		flog(inputdir, "does not exist.  See wpp -help.")
	} else if !stat.IsDir() {
		flog(inputdir, "is not a directory.  See wpp -help.")
	}

	if OptTemplate != "" {
		html, err = loadHtml(OptTemplate)
		if err != nil {
			flog(err)
		}
	} else {
		html = ProgHtmlTemplate
	}

	if OptOutfile == "" {
		out = io.Writer(os.Stdout)
	} else {
		dir := filepath.Dir(OptOutfile)

		if dir == OptOutfile[:len(OptOutfile)-1] {
			flog(OptOutfile, "is a directory path. Not a file.")
		} else if dir != "." {
			if e := os.MkdirAll(dir, os.ModeDir|os.ModePerm); e != nil {
				flog("Could not make directory for", OptOutfile, " --", e)
			}
		}

		file, err = os.Create(OptOutfile)
		if err != nil {
			flog("Could not create file", OptOutfile, "--", err)
		}
		defer file.Close()

		out = file
	}

	if OptDevmode {
		var (
			isReady     = true
			pending     = false
			interrupted = false
			served      = false
			ready       = make(chan struct{})
			done        = make(chan struct{})
			interrupt   = make(chan os.Signal, 1)
			ignore      *regexp.Regexp
		)
		defer close(done)

		if OptOutfile == "" {
			vlog("Dev mode with no outfile can not serve files and hot reload.")
		}

		updates, err := filewatch.Watch(done, inputdir, true, nil)
		if err != nil {
			flog("Could not watch", inputdir, "directory --", err)
		}
		// Skip initial updates as the initial template update will
		// drive the first output.
		<-updates

		var tmplUpdate <-chan []filewatch.Update
		if OptTemplate != "" {
			tmplUpdate, err = filewatch.Watch(done, OptTemplate, false, nil)
			if err != nil {
				flog("Could not watch template file, ", OptTemplate, " --", err)
			}
		} else {
			ch := make(chan []filewatch.Update, 1)
			ch <- make([]filewatch.Update, 0)
			tmplUpdate = (<-chan []filewatch.Update)(ch)
		}

		if OptIgnore != "" {
			var rerr error
			if ignore, rerr = regexp.Compile(OptIgnore); rerr != nil {
				elog("Failed to compile regexp for", OptIgnore, " --", rerr)
			}
		}

		signal.Notify(interrupt, os.Interrupt)

		for !interrupted {
			select {
			case us := <-updates:
				for _, u := range us {
					if !u.Prev.IsDir() && !u.WasRemoved {
						name := u.Prev.Name()
						if ignore != nil && ignore.MatchString(name) {
							continue
						}

						ext := strings.ToLower(filepath.Ext(name))
						old := pending
						pending = pending || ext == ".js" || ext == ".css"
						if !old && pending {
							vlog("Detected change of file", name)
						}
					}
				}
			case <-tmplUpdate:
				pending = true
				if OptTemplate != "" {
					vlog("Detected change of HTML template:", OptTemplate)

					html, err = loadHtml(OptTemplate)
					if err != nil {
						elog(err)
					}
				}
			case <-ready:
				vlog("Finished processing file changes, set isReady to true")
				isReady = true
			case <-interrupt:
				interrupted = true
			}

			if isReady && pending && !interrupted {
				isReady = false
				pending = false

				go (func() {
					var (
						err  error
						port uint
					)

					if OptOutfile != "" {
						port = OptDevport

						if err = file.Truncate(0); err != nil {
							elog("Failed to truncate outfile,", OptOutfile, "-- ", err)
						} else if _, err = file.Seek(0, 0); err != nil {
							elog("Failed to seek to beginning of outfile,", OptOutfile, "-- ", err)
						} else {
							defer (func() {
								if err = file.Sync(); err != nil {
									elog("Failed to sync outfile,", OptOutfile, "-- ", err)
								}
							})()
						}
					}

					if err == nil {
						if err = preprocess(inputdir, html, out, port); err != nil {
							elog("Failed to pre-process", inputdir, " --", err)
						} else if port > 0 {
							if !served {
								served = true

								http.HandleFunc("/", index)
								http.HandleFunc("/wpphotreload", reload)

								go (func() {
									err = http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
									elog("Failed to start HTTP web server on localhost --", err)
								})()

								cmd := exec.Command(OpenBrowserCommand, OptOutfile)
								if err = cmd.Run(); err != nil {
									elog("Failed to open", OptOutfile, "in browser --", err)
								} else {
									vlog(fmt.Sprintf(`Opening in browser with "%s %s"`,
										OpenBrowserCommand,
										OptOutfile))
								}
							} else if ProgWebSocket != nil {
								msgt := websocket.TextMessage
								msg := []byte("reload")

								if err = ProgWebSocket.WriteMessage(msgt, msg); err != nil {
									elog(`Failed to write "reload" web socket message`, err)
								}
							} else {
								elog("ProgWebSocket is nil, can't write messages")
							}
						} else {
							fmt.Println() // additional newline
						}
					}
					ready <- struct{}{}
				})()
			}
		}
		fmt.Println()
		vlog("Dev mode exited cleanly")
	} else if err := preprocess(inputdir, html, out, 0); err != nil {
		flog("Failed to pre-process", inputdir, " --", err)
	}
}

// Pre-process the files from indir and writes the output to
// out.  All the contents from the files in indir will be spliced
// into the html template.
func preprocess(indir, html string, out io.Writer, reloadPort uint) error {
	const (
		MinUint = uint(0)
		MaxUint = ^MinUint
		MaxInt  = int(MaxUint >> 1)
	)

	var (
		result struct {
			CSS        string
			Javascript string
		}
		js  bytes.Buffer
		css bytes.Buffer
	)

	tmpl, err := template.New("html").Parse(html)
	if err != nil {
		return err
	}

	css.WriteString(`<style type="text/css">`)
	js.WriteString(`<script type="text/javascript">`)

	err = filepath.Walk(indir, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}

		var pbuf *bytes.Buffer

		switch strings.ToLower(filepath.Ext(path)) {
		case ".js":
			pbuf = &js
		case ".css":
			pbuf = &css
		default:
			pbuf = nil
		}

		if pbuf != nil {
			file, err := os.Open(path)
			if os.IsNotExist(err) {
				return nil
			} else if err != nil {
				return err
			}
			defer file.Close()

			sz := info.Size()
			if sz >= int64(MaxInt) {
				return fmt.Errorf("Files larger than %v are not supported.", MaxInt)
			}
			pbuf.Grow(int(sz))
			io.Copy(pbuf, file)
		}

		return nil
	})
	if err != nil {
		return err
	}

	if reloadPort > 0 {
		reload, err := template.New("reload").Parse(ProgHotReloadCode)
		if err != nil {
			return err
		} else if err := reload.Execute(&js, reloadPort); err != nil {
			return err
		}
	}

	css.WriteString("</style>")
	js.WriteString("</script>")
	result.CSS = css.String()
	result.Javascript = js.String()

	if err := tmpl.Execute(out, result); err != nil {
		return err
	}

	return nil
}

func loadHtml(file string) (string, error) {
	_, err := os.Stat(file)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("%s does not exist", file)
	} else if err != nil {
		return "", fmt.Errorf("Could not read file info for %s -- %v", file, err)
	}

	b, err := ioutil.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("Could not read template file, %s -- %v", file, err)
	}
	return string(b), nil
}

func index(w http.ResponseWriter, r *http.Request) {
	if OptOutfile != "" {
		http.ServeFile(w, r, OptOutfile)
	}
}

func reload(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	var err error
	ProgWebSocket, err = upgrader.Upgrade(w, r, nil)
	if err != nil {
		elog("Could not updgrade HTTP request to websocket --", err)
		return
	}
	defer ProgWebSocket.Close()

	for {
		msgtype, msg, err := ProgWebSocket.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				vlog("Websocket connection closed --", err)
				return
			} else {
				elog("Error reading web socket message --", err)
			}
		} else {
			var result string

			switch msgtype {
			case websocket.TextMessage:
				result = fmt.Sprintf("Received web socket text message: %s", msg)
			case websocket.BinaryMessage:
				result = "Received web socket binary message"
			case websocket.CloseMessage:
				result = "Received web socket close message"
			case websocket.PingMessage:
				result = "Received web socket ping message"
			case websocket.PongMessage:
				result = "Received web socket pong message"
			}
			vlog(result)
		}
	}
}

// For logging errors.
func elog(args ...interface{}) {
	post := fmt.Sprintln(args...)
	log.Output(2, ProgName+" [ERROR] "+post)
}

// For logging fatal errors.
func flog(args ...interface{}) {
	post := fmt.Sprintln(args...)
	log.Output(2, ProgName+" [FATAL] "+post)
	os.Exit(1)
}

// For logging verbose output.
func vlog(args ...interface{}) {
	if OptVerbose {
		post := fmt.Sprintln(args...)
		log.Output(2, ProgName+" [VERBOSE] "+post)
	}
}

const (
	ProgName         = "[wpp]"
	ProgHtmlTemplate = `<!doctype html>
<html>
  <head>
    <meta charset="utf-8">
    {{.CSS}}
  </head>
  <body></body>
  {{.Javascript}}
</html>`

	ProgHotReloadCode = `
(function () {
    window.addEventListener("load", function(evt) {
        var socket = new WebSocket('ws://localhost:{{.}}/wpphotreload');
        socket.addEventListener('message', function(wsevt) {
            if (wsevt.data === 'reload') {
                console.log("File change detected, reloading page.");
                window.location.reload(true);
            }
        });
    });
})()`

	UsageHelp     = "prints this help"
	UsageVerbose  = "print wpp's log output"
	UsageOutfile  = "name of output file"
	UsageTemplate = "template HTML file to use"
	UsageIgnore   = "regex of files to ignore from inputdir"
	UsageProgram  = `wpp [options] inputdir

Wpp is a web pre-processor that reads web files from 'inputdir' and
takes the contents of all Javascript and CSS files and embeds the
contents into a single HTML file.  Wpp does not perform any
transformation on the input and instead relies on other tools to
perform such tasks.

The final contents will be insterted into the template file specified
by -template command line flag.  This template file is expected to be
an HTML file with two locations to insert the CSS and Javascript.  For
example, suppose we have a template named index-template.html that
contains:

    <!doctype html>
    <html>
      <head>
        {{.CSS}}
        {{.Javascript}}
      </head>
      <body>
        <h1>Hello, world!</h1>
      </body>
    </html>

then wpp will insert all CSS content where '{{.CSS}}' tag is and
surround the content with an appropriate style HTML tag, and will
insert all Javascript where the '{{.Javascript}}' tag is and surround
the content with an appropriate script HTML tag.

If a template file is not provided then wpp will provide a default
that looks like:

    <!doctype html>
    <html>
      <head>
        <meta charset="utf-8">
        {{.CSS}}
        {{.Javascript}}
      </head>
      <body></body>
    </html>

Note that wpp uses the text/template package Go lang's standard
library to perform the text substitution and that assumes the inserted
content is trusted as it was strictly written by the developer.  Wpp
wasn't designed to process end user content; it is merely a
pre-processor.

Finally, it should be noted that wpp provides a developer mode where
it will watch the given input directory and the template file for any
file changes and continually process the input as it changes.
Furthermore, wpp will insert a small snippet of Javascript into the
output to allow hot reloading with the browser.  By default when the
devmode flag is set wpp will serve the final HTML output via port 8082
on localhost unless a different port is specified as an argument to
the devport flag.  If devport is set to 0 or outfile is not set then
then devmode no longer serves HTML and perform hot reloading; instead,
it merely watches inputdir and dumps the output to stdout.

Wpp builds a single HTML file whose name is specified by the outfile
flag.  If the outfile flag is not specified then the output will be
sent to standard out.  Note that the output flag can specify a path
to the output file name with directories don't exist which will then
be created.  For example, -output 'build/index.html' will create a
directory named 'build' where wpp was called if it doesn't exist and
place the output into index.html inside that directory.

Wpp provides the following options:
`
)
