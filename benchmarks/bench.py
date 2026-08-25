#!/usr/bin/env python3
"""o- vs bun head-to-head benchmark.

Runs the SAME developer workflows in both ecosystems and measures them:
  scaffold, cold start (dev server up), hot reload (edit -> new content),
  build (single-file artifact), test.

Honest framing: bun is a JS/TS runtime, o- is a Go toolchain. This measures
workflow DX parity, not language speed. Results are written to
benchmarks/results/bench-<timestamp>.json (and printed as a table).

Usage: python3 benchmarks/bench.py [--keep]
"""

import json
import os
import signal
import subprocess
import sys
import tempfile
import time
from datetime import datetime, timezone
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
O_BIN = REPO / "bin" / "o-"
RESULTS_DIR = REPO / "benchmarks" / "results"
GO_PORT = 8311
BUN_PORT = 8312

GO_MAIN = """package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func greeting() string { return "greet-v1" }

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "o- says: %s\\n", greeting())
	})
	log.Printf("listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
"""

GO_TEST = """package main

import "testing"

func TestGreeting(t *testing.T) {
	if greeting() != "greet-v1" {
		t.Fatal("wrong greeting")
	}
}
"""

GO_MANIFEST = """name: bench-go
version: "0.1.0"
type: app

run:
  watch:
    - ./**/*.go

build:
  output: ./dist/bench-go

bundle:
  include:
    - templates/**/*
"""

BUN_SERVER = """const greeting = () => "greet-v1";

const server = Bun.serve({
  port: Number(process.env.PORT || 8080),
  fetch(req) {
    return new Response("bun says: " + greeting() + "\\n");
  },
});

console.log("listening on :" + server.port);
"""

BUN_TEST = """import { test, expect } from "bun:test";

test("greeting", () => {
  expect(greeting()).toBe("greet-v1");
});
"""

BUN_PKG = """{
  "name": "bench-bun",
  "version": "0.1.0",
  "module": "server.ts",
  "type": "module",
  "devDependencies": {}
}
"""

BUN_TS_CONFIG = """{
  "compilerOptions": { "lib": ["ESNext"], "types": ["bun"] }
}
"""


def log(msg: str) -> None:
    print(f"[bench] {msg}", flush=True)


def run(cmd, cwd=None, timeout=120, env=None) -> subprocess.CompletedProcess:
    full_env = dict(os.environ)
    if env:
        full_env.update(env)
    return subprocess.run(cmd, cwd=cwd, env=full_env, capture_output=True,
                          text=True, timeout=timeout)


def wait_http(port: int, want: str, timeout: float = 30.0) -> float:
    """Poll GET until the response contains `want`. Returns seconds elapsed."""
    import urllib.request

    start = time.perf_counter()
    deadline = start + timeout
    while time.perf_counter() < deadline:
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{port}/", timeout=1) as r:
                body = r.read().decode()
                if want in body:
                    return time.perf_counter() - start
        except Exception:
            pass
        time.sleep(0.05)
    raise TimeoutError(f"port {port} never served {want!r} within {timeout}s")


def start_server(cmd, cwd, port, env=None):
    full_env = dict(os.environ)
    if env:
        full_env.update(env)
    full_env["PORT"] = str(port)
    p = subprocess.Popen(cmd, cwd=cwd, env=full_env,
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                         start_new_session=True)
    return p


def stop_server(p) -> None:
    if p and p.poll() is None:
        try:
            os.killpg(os.getpgid(p.pid), signal.SIGTERM)
        except Exception:
            p.terminate()
        try:
            p.wait(timeout=5)
        except Exception:
            try:
                os.killpg(os.getpgid(p.pid), signal.SIGKILL)
            except Exception:
                pass


def build_go_app(ws: Path) -> Path:
    app = ws / "bench-go"
    log("scaffolding go app (o- new)")
    r = run([str(O_BIN), "new", "bench-go", "--template", "web-server"], cwd=ws)
    if r.returncode != 0:
        raise SystemExit(f"o- new failed: {r.stderr}")
    (app / "main.go").write_text(GO_MAIN)
    (app / "main_test.go").write_text(GO_TEST)
    (app / "o-.yaml").write_text(GO_MANIFEST)
    tmpl = app / "templates"
    tmpl.mkdir(exist_ok=True)
    (tmpl / "hello.html").write_text("embedded-template-v1")
    return app


def build_bun_app(ws: Path) -> Path:
    app = ws / "bench-bun"
    app.mkdir()
    log("scaffolding bun app (bun init -y)")
    r = run(["bun", "init", "-y"], cwd=app, timeout=60)
    if r.returncode != 0:
        raise SystemExit(f"bun init failed: {r.stderr}")
    (app / "server.ts").write_text(BUN_SERVER)
    (app / "server.test.ts").write_text(BUN_TEST)
    (app / "package.json").write_text(BUN_PKG)
    (app / "tsconfig.json").write_text(BUN_TS_CONFIG)
    return app


def measure_scaffold(fn, ws: Path) -> tuple[float, Path]:
    start = time.perf_counter()
    app = fn(ws)
    return time.perf_counter() - start, app


def measure_cold_start(app: Path, cmd, port, env=None) -> float:
    log(f"cold start: {' '.join(map(str, cmd))}")
    p = start_server(cmd, app, port, env)
    try:
        return wait_http(port, "says: greet-v1")
    finally:
        stop_server(p)


def measure_hot_reload(app: Path, cmd, port, edit, env=None) -> float:
    log(f"hot reload: {os.path.basename(str(cmd[0]))}")
    p = start_server(cmd, app, port, env)
    try:
        wait_http(port, "greet-v1")
        edit()
        return wait_http(port, "greet-v2")
    finally:
        stop_server(p)


def measure_build(app: Path, cmd, out_name) -> tuple[float, int]:
    log(f"build: {' '.join(map(str, cmd))}")
    start = time.perf_counter()
    r = run(cmd, cwd=app, timeout=180)
    elapsed = time.perf_counter() - start
    if r.returncode != 0:
        raise SystemExit(f"build failed ({cmd}): {r.stderr[-2000:]}")
    artifacts = list(app.rglob(out_name))
    size = artifacts[0].stat().st_size if artifacts else 0
    return elapsed, size


def measure_test(app: Path, cmd, want_ok=True) -> tuple[float, bool]:
    log(f"test: {' '.join(map(str, cmd))}")
    start = time.perf_counter()
    r = run(cmd, cwd=app, timeout=180)
    elapsed = time.perf_counter() - start
    passed = r.returncode == 0 if want_ok else r.returncode != 0
    return elapsed, passed


def main() -> None:
    keep = "--keep" in sys.argv
    RESULTS_DIR.mkdir(parents=True, exist_ok=True)
    ws = Path(tempfile.mkdtemp(prefix="o-bench-"))
    results: dict = {"ts": datetime.now(timezone.utc).isoformat(),
                     "o_version": run([str(O_BIN), "--version"]).stdout.strip(),
                     "bun_version": run(["bun", "--version"]).stdout.strip(),
                     "metrics": {}}
    try:
        # --- go side ---
        t, go_app = measure_scaffold(build_go_app, ws)
        results["metrics"]["go_scaffold_s"] = round(t, 3)
        results["metrics"]["go_cold_start_s"] = round(
            measure_cold_start(go_app, [str(O_BIN), "run"], GO_PORT,
                               env={"XDG_CACHE_HOME": str(ws / "cache-go")}), 3)

        def edit_go():
            (go_app / "main.go").write_text(GO_MAIN.replace("greet-v1", "greet-v2"))

        results["metrics"]["go_hot_reload_s"] = round(
            measure_hot_reload(go_app, [str(O_BIN), "run"], GO_PORT, edit_go,
                               env={"XDG_CACHE_HOME": str(ws / "cache-go")}), 3)
        results["metrics"]["go_build_s"], go_size = measure_build(
            go_app, [str(O_BIN), "build"], "bench-go")
        results["metrics"]["go_build_size_bytes"] = go_size
        results["metrics"]["go_test_s"], go_test_ok = measure_test(
            go_app, [str(O_BIN), "test"])
        results["metrics"]["go_test_passed"] = go_test_ok

        # --- bun side ---
        t, bun_app = measure_scaffold(build_bun_app, ws)
        results["metrics"]["bun_scaffold_s"] = round(t, 3)
        results["metrics"]["bun_cold_start_s"] = round(
            measure_cold_start(bun_app, ["bun", "run", "server.ts"], BUN_PORT), 3)

        def edit_bun():
            (bun_app / "server.ts").write_text(BUN_SERVER.replace("greet-v1", "greet-v2"))

        results["metrics"]["bun_hot_reload_s"] = round(
            measure_hot_reload(bun_app, ["bun", "--hot", "server.ts"], BUN_PORT,
                               edit_bun), 3)
        results["metrics"]["bun_build_s"], bun_size = measure_build(
            bun_app, ["bun", "build", "--compile", "--outfile", "bench-bun",
                      "server.ts"], "bench-bun")
        results["metrics"]["bun_build_size_bytes"] = bun_size
        results["metrics"]["bun_test_s"], bun_test_ok = measure_test(
            bun_app, ["bun", "test"])
        results["metrics"]["bun_test_passed"] = bun_test_ok
    finally:
        if not keep:
            import shutil
            shutil.rmtree(ws, ignore_errors=True)

    stamp = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")
    out_file = RESULTS_DIR / f"bench-{stamp}.json"
    out_file.write_text(json.dumps(results, indent=2))

    m = results["metrics"]
    print("\n=== o- vs bun (workflow DX) ===")
    print(f"  o-  {results['o_version']}   |   bun {results['bun_version']}")
    print(f"{'metric':<20}{'o- (go)':>14}{'bun':>14}{'delta':>14}")
    pairs = [
        ("scaffold (s)", m["go_scaffold_s"], m["bun_scaffold_s"], False),
        ("cold start (s)", m["go_cold_start_s"], m["bun_cold_start_s"], False),
        ("hot reload (s)", m["go_hot_reload_s"], m["bun_hot_reload_s"], False),
        ("build (s)", m["go_build_s"], m["bun_build_s"], False),
        ("artifact (MB)", m["go_build_size_bytes"] / 1e6, m["bun_build_size_bytes"] / 1e6, False),
        ("test (s)", m["go_test_s"], m["bun_test_s"], False),
    ]
    for name, a, b, _ in pairs:
        delta = (a - b) / b * 100 if b else 0
        print(f"{name:<20}{a:>12.2f}  {b:>12.2f}  {delta:>+11.0f}%")
    print(f"\nresults saved to {out_file}")


if __name__ == "__main__":
    main()
