# The real shape a founder hit: ubx.stack(...) BUILDS a definition and
# returns it, so this program exits 0 having written nothing. The stderr
# line is here to prove it survives a zero exit.
import sys

import ubx_sdk as sdk


def describe():
    sdk.intent("never evaluated, because run() is never called")


if __name__ == "__main__":
    print("a diagnosis written by a program that then exited 0", file=sys.stderr)
    sdk.stack("payments", describe)
