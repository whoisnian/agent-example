"""Allows ``python -m worker`` as an alias for the registered ``worker`` script."""

from worker.main import run

if __name__ == "__main__":
    run()
