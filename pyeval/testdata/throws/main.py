import ubx_sdk as sdk


def describe():
    sdk.intent("about to raise")
    raise RuntimeError("deliberate mid-evaluation exception")


if __name__ == "__main__":
    sdk.run("payments", describe)
