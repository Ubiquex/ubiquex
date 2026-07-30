import socket
import sys

try:
    s = socket.create_connection(("8.8.8.8", 53), timeout=3)
    s.close()
    print("sandbox_net: dial unexpectedly succeeded", file=sys.stderr)
    sys.exit(1)
except Exception as e:
    print(f"sandbox_net: dial 8.8.8.8:53: {type(e).__name__}: {e}", file=sys.stderr)
    sys.exit(1)
