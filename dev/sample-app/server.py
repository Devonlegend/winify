import http.server
import os
import socketserver

PORT = int(os.environ.get("PORT", "18080"))


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = b"hello from the control-center demo app\n"
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


socketserver.TCPServer.allow_reuse_address = True
with socketserver.TCPServer(("0.0.0.0", PORT), Handler) as httpd:
    httpd.serve_forever()
