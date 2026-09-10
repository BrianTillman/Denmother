"""Check release assets and local Markdown links against Git's public file set."""

import os
from pathlib import Path
import re
import subprocess
import sys
from urllib.parse import unquote, urlsplit


def check(root):
    names = subprocess.check_output(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
        cwd=root,
    )
    public = {os.fsdecode(name) for name in names.split(b"\0") if name}
    errors = []
    for name in (root / "scripts/release-assets.txt").read_text().split():
        if name not in public or not (root / name).is_file():
            errors.append(f"release asset is absent from public source: {name}")
        elif (root / name).is_symlink():
            errors.append(f"release asset is a symlink: {name}")

    for name in sorted(public):
        path = root / name
        if path.suffix != ".md" or not path.is_file():
            continue
        # Exclude fenced examples before scanning inline Markdown links.
        prose = re.sub(r"(?ms)^```.*?^```[^\n]*", "", path.read_text())
        for link in re.findall(r"\[[^\]\n]*\]\(([^)\s]+)\)", prose):
            url = urlsplit(link.strip("<>"))
            if url.scheme or url.netloc or not url.path:
                continue
            target = os.path.normpath(str(Path(name).parent / unquote(url.path)))
            if target not in public:
                errors.append(f"{name}: link target is absent from public source: {target}")
    return errors


if __name__ == "__main__":
    root = Path(__file__).resolve().parent.parent
    errors = check(root)
    for error in errors:
        print(error, file=sys.stderr)
    if errors:
        sys.exit(1)
    print("Release assets and local documentation links are in public source.")
