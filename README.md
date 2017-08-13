wpp
===

Wpp is a web pre-processor that takes multiple css and javascript
files to assemble them into a single HTML file.

Overview
--------

Wpp is like [webpack](https://webpack.js.org) but much simpler.  It
requires no configuration files and just uses a few command line flags
for some basic options.  Furthermore, instead on assembling an HTML
file that refers to a build.js and/or a vendor.js file, wpp inlines
all Javascript and CSS directly to into output HTML file.  Of course,
this has advantages and disadvantages but wpp is meant for simple
processing when you want to build a single page application delivered
in a single file and not hassle with any complex configuration.

Wpp does not perform any transformations on the input javascript and
css files.  Instead, it is intended to really on other tools that do
a better job and encourages composibility.  For example, you may want
pass all Javascript files through a specific minifier and have SCSS
or LESS files processed through their specific pre-processors.

Finally, wpp provides a development server.  Passing the `-devmode`
flag to wpp will open the assembled HTML file in your default browser
and when wpp detects any changes in Javascript or CSS files from the
input directory it will automatically refresh the browser page to
hot reload the contents.

Building
--------

Wpp has the following dependencies:

* [filewatch](https://www.github.com/0xABAD/filewatch)
* [websocket](https://www.github.com/gorilla/websocket)

Run `go get` for those packages and then `go install` for this one to
build this repo.

Usage
-----

For the most basic usage of wpp (options can be preceeded by one or
two dashes):

```
$ cd /path/to/website/
$ wpp -template index_template.html -outfile build/index.html src
```

Here `index_template.html` is the template file that contains specific
markup of where to inline CSS and Javascript.  If no template is
provided then wpp will use a default template for you.  The output is
placed in `build/index.html` which will contain the assembled output.
If the `build` directory doesn't exist it will be created for you.  If
no outfile is specified then the output will be dumped to stdout.

To run the development server then run wpp as:

```
$ wpp -template index-template.html -outfile build/index.html -devmode src
```

Now wpp will continously watch the files in `src` for any changes,
update `build/index.html`, and hot reload the assembled HTML file in
your default web browser.

The full help can be accessed directly from wpp by running `wpp -help`.  Also,
the `web` folder in this repo provides some test files to play around with.

Known Issues
------------

Wpp hasn't been tested on a Linux distro.  Everything *should* work
but opening the assembled HTML output in the browser might not.
Currently, it is set to use `xdg-open` but according to this
[stackoverflow](https://stackoverflow.com/questions/5116473/linux-command-to-open-url-in-default-browser)
post this method doesn't work on all distros.  I'm open to more robust
solutions if you have one.

License
-------

Zlib
