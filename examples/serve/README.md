# serve — demo app built with o-

A zero-config static file server with directory listing and live reload,
built entirely with o-: `o- new`, `o- run`, `o- test`, `o- bundle`, `o- build`.

The tool every developer reaches for (`python -m http.server`, `npx serve`)
but as ONE static binary with a live-reload client embedded inside it.

## Features

- Serve any directory: `serve ./public`
- Directory listing with safe HTML-escaped links
- Live reload: HTML responses get a small injected client that polls
  `/__o_reload` and reloads the page when any file under the root changes
- Path traversal blocked (symlink-aware, tested)
- No dependencies, one binary, works offline

## Build it yourself (this is the demo)

    o- new serve --template web-server     # scaffold
    o- bundle                              # embed templates/livereload.js into the binary
    o- test                                # 6 tests: serving, injection, traversal, listing
    o- run -- ./public                     # dev: hot reload while you edit
    o- build                               # single static binary -> dist/serve

## Try it

    ./dist/serve /tmp/serve-demo
    # open http://localhost:8080 — edit a file in /tmp/serve-demo and watch
    # the browser reload itself.

## Layout

    main.go                 the server (file serving, listing, reload endpoint)
    main_test.go            unit tests for the security-critical paths
    templates/livereload.js embedded via o- bundle -> served at /__o_reload.js
    o-.yaml                 manifest: run watch rules + bundle.include

## What this shows about o-

- `o- new` scaffolded the project; `o- run` gave hot reload during development
- `o- bundle` embedded the reload client INTO the binary (single-file deploy)
- `o- build` produced a 5.3 MiB static binary with a verified cache
- `o- test` ran the suite with a friendly UI
- arg passthrough: `o- run -- <args>` forwards args to your app
