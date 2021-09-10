This application just listens for http requests and forwards them to a remote server, changing the Content-Type of POST requests to "application/json".

Usage:

typeproxy [-port Port] [-grace Grace] backendURL

- `-port` or environment variable `TYPEPROXY_PORT`: TCP Port to listen to, must be >= 1024.
- `-grace` or environment variable `TYPEPROXY_GRACE`: Grace interval to wait for connections to close, when shutting down.
- `backendURL` or environment variable `TYPEPROXY_URL`: Backend URL to forward requests to.
