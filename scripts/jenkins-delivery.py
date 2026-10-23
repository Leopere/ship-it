#!/usr/bin/env python3
"""Submit or observe the exact pushed CLI revision; Jenkins owns installation."""
import os
from pathlib import Path
import re
import subprocess
import sys


def main():
    revision = os.environ.get("DEPLOY_IT_COMMIT", "")
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise SystemExit("A full DEPLOY_IT_COMMIT is required")
    controller = Path.home() / ".local/share/jenkins-local/one-shot-tally-delivery.py"
    if not controller.is_file():
        raise SystemExit("Jenkins hook CLI delivery is not installed: " + str(controller))
    result = subprocess.run([sys.executable, str(controller), "deploy", "--profile", "ship-it", "--revision", revision], timeout=295)
    raise SystemExit(result.returncode)


if __name__ == "__main__":
    main()
