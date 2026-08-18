#!/usr/bin/env python3
"""本地起站点：先重建 site/，再用标准库 HTTP server 提供服务。

用法:
    python3 tools/serve_site.py [--port 8767] [--host 127.0.0.1]
"""
from __future__ import annotations

import argparse
import functools
import http.server
import socketserver
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SITE = ROOT / "site"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=8767)
    args = ap.parse_args()

    rc = subprocess.run([sys.executable, str(ROOT / "tools" / "build_site.py")]).returncode
    if rc != 0:
        return rc

    handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=str(SITE))

    class Server(socketserver.TCPServer):
        allow_reuse_address = True

    with Server((args.host, args.port), handler) as httpd:
        print(f"开发工作台：http://{args.host}:{args.port}/  （Ctrl-C 停止）")
        try:
            httpd.serve_forever()
        except KeyboardInterrupt:
            print("\n已停止。")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
