import sys

try:
    with open("/etc/hosts") as f:
        data = f.read()
    print(f"sandbox_fs: read unexpectedly succeeded: {len(data)} bytes", file=sys.stderr)
    sys.exit(1)
except Exception as e:
    print(f"sandbox_fs: read /etc/hosts: {type(e).__name__}: {e}", file=sys.stderr)
    sys.exit(1)
