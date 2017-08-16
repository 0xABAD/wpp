// Programatically get and install dependencies for a package.
package main

// Released under the public domain and the MIT license for
// entities that do not recognize works under the public domain.

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
)

var dependencies = [...]Dep{
	Dep{Repo: "github.com/0xABAD/filewatch", Branch: "v0.1.0"},
	Dep{Repo: "github.com/gorilla/websocket", Branch: "v1.2.0"},
}

type Dep struct {
	// (Required) Name of the repository which may or may not be a
	// go package.
	Repo string

	// (Optional) Name of branch, tag, or commit to checkout from
	// Repo.  If no value is given then the master branch will be
	// used for building the package.
	Branch string

	// (Optionaly) A directory to clone the repository to.  If no
	// value is given then $GOPATH will be used.
	CloneDir string

	// (Optional) A function that clones the repository defined by
	// the value repo and clones the repository to cloneDir using
	// some VCS.  If nil then 'go get' will be used unless the
	// CloneDirectory field is specified which then main.gitClone
	// function will be used.
	VcsCloneRepoFunc func(repo, cloneDir string) error

	// (Optional) A function that checks out branch for the retrieved
	// repository.  If nil then main.gitCheckout will be used.  Note,
	// that this function is only called if a value is given to the
	// Branch field.
	VcsCheckoutFunc func(branch string) error

	// (Optional) A function to call after the dependency has been
	// retrieved and the particular branch has been checked out.  If
	// nil then 'go install' will be used.
	BuildFunc func() error
}

func main() {
	flag.Parse()

	if flagHelp {
		flag.Usage()
		os.Exit(0)
	}

	pwd, err := os.Getwd()
	if err != nil {
		flog("Can't get current working directory --", err)
	}
	vlog("Begin running getand/install from directory:", pwd)

	var (
		deplen   = len(dependencies)
		done     = make(chan bool, deplen)
		checkout = make(chan struct {
			Dep
			hasError chan<- bool
		})
	)

	// We don't want multiple dependencies to change the directory to
	// different locations at the same time so this go routine will
	// handle checkouts and installs here.
	go func() {
		for co := range checkout {
			var (
				cofn = gitCheckout
				dir  string
			)

			if co.VcsCheckoutFunc != nil {
				cofn = co.VcsCheckoutFunc
			}

			if co.CloneDir != "" {
				dir = co.CloneDir
			} else {
				dir = fmt.Sprintf("%s/src/%s", os.Getenv("GOPATH"), co.Repo)
			}

			vlog("Changing to directory", dir)
			if err := os.Chdir(dir); err != nil {
				elog("Failed to change to directory", dir, " --", err)
				co.hasError <- true
				continue
			}

			hasError := false
			if co.Branch != "" {
				vlog("Switching to branch/tag/commit", co.Branch, "for", co.Repo)
				if !flagNoExecute {
					if err := cofn(co.Branch); err != nil {
						elog("Could not checkout branch", co.Branch, " --", err)
						hasError = true
					}
				}
			}

			if !hasError {
				if co.BuildFunc != nil {
					vlog("Installing with custom build function for", co.Repo)
					if !flagNoExecute {
						if err := co.BuildFunc(); err != nil {
							elog(`"Custom build function failed for package`, co.Repo, " --", err)
							hasError = true
						}
					}
				} else {
					vlog(`Installing with "go install" for`, co.Repo)
					if !flagNoExecute {
						cmd := exec.Command("go", "install")
						if err := cmd.Run(); err != nil {
							elog(`"go install" failed for package`, co.Repo, " --", err)
							hasError = true
						}
					}
				}
			}

			vlog("Changing to directory", pwd)
			if err := os.Chdir(pwd); err != nil {
				flog("Failed to change to directory", pwd, " installation unrecoverable --", err)
			}

			co.hasError <- hasError
		}
	}()

	// Get or clone each dependency in a separate go routine and
	// hand off checkout and installation in the go routine above.
	for _, d := range dependencies {
		dep := d

		go func() {
			if dep.VcsCloneRepoFunc != nil {
				vlog("Cloning with custom VCS clone function for repo:", dep.Repo)
				if !flagNoExecute {
					if err := dep.VcsCloneRepoFunc(dep.Repo, dep.CloneDir); err != nil {
						elog("Failed to clone repositority with custom VCS clone function --", err)
						done <- true
						return
					}
				}
			} else if dep.CloneDir != "" {
				vlog(`Running "git clone" for`, dep.Repo, "and cloning into", dep.CloneDir, "directory")
				if !flagNoExecute {
					if err := gitClone(dep.Repo, dep.CloneDir); err != nil {
						elog(`Failed to "git clone"`, dep.Repo, "into", dep.CloneDir, "directory --", err)
						done <- true
						return
					}
				}
			} else {
				vlog(fmt.Sprintf(`Running "go get -d %s"`, dep.Repo))
				if !flagNoExecute {
					cmd := exec.Command("go", "get", "-d", dep.Repo)
					if err := cmd.Run(); err != nil {
						elog(`"go get -d" for package`, dep.Repo, "failed --", err)
						done <- true
						return
					}
				}
			}

			result := make(chan bool, 1)
			checkout <- struct {
				Dep
				hasError chan<- bool
			}{dep, result}

			select {
			case hasError := <-result:
				if hasError {
					done <- true
					return
				}
			}

			done <- false
		}()
	}

	hasError := false
	for i := 0; i < deplen; i++ {
		select {
		case e := <-done:
			hasError = hasError || e
		}
	}
	if hasError {
		flog("Errors encountered during dependency installation.")
	}

	vlog(`Dependencies installed, running "go install" for this package`)
	if !flagNoExecute {
		cmd := exec.Command("go", "install")
		if err := cmd.Run(); err != nil {
			flog(`Failed installation with "go install" --`, err)
		}
	}
}

func gitClone(repo, cloneDir string) error {
	url := "https://" + repo
	cmd := exec.Command("git", "clone", "--recursive", url, cloneDir)
	return cmd.Run()
}

func gitCheckout(branch string) error {
	cmd := exec.Command("git", "checkout", branch)
	return cmd.Run()
}

var (
	flagHelp      bool
	flagVerbose   bool
	flagNoExecute bool
)

const (
	progName = "[GAI]"

	usageHelp      = "Print this help"
	usageVerbose   = "Print verbose output"
	usageNoExecute = "Don't execute actual commands, implies verbose"
)

func init() {
	flag.BoolVar(&flagHelp, "help", false, usageHelp)
	flag.BoolVar(&flagHelp, "h", false, usageHelp)
	flag.BoolVar(&flagVerbose, "verbose", false, usageVerbose)
	flag.BoolVar(&flagVerbose, "v", false, usageVerbose)
	flag.BoolVar(&flagNoExecute, "noexecute", false, usageNoExecute)
	flag.BoolVar(&flagNoExecute, "n", false, usageNoExecute)

	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "USAGE: go run getand/install.go\n")
		flag.PrintDefaults()
	}

	log.SetFlags(log.Ltime)
}

// For logging errors.
func elog(args ...interface{}) {
	post := fmt.Sprintln(args...)
	log.Output(2, progName+" [ERROR] "+post)
}

// For logging fatal errors.
func flog(args ...interface{}) {
	post := fmt.Sprintln(args...)
	log.Output(2, progName+" [FATAL] "+post)
	os.Exit(1)
}

// For logging verbose output.
func vlog(args ...interface{}) {
	post := fmt.Sprintln(args...)
	if flagNoExecute {
		log.Output(2, progName+" [INFO] "+post)
	} else if flagVerbose {
		log.Output(2, progName+" [VERBOSE] "+post)
	}
}
