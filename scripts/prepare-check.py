#!/usr/bin/env python3
"""Fetch the official, checksum-verified frps used only by CLI integration checks."""
import hashlib
import io
import json
import pathlib
import platform
import tarfile
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parent.parent
ARCH = "arm64" if platform.machine() == "arm64" else "amd64"
VERSION = "0.72.0"


def main():
    target = ROOT / ".test-tools" / "frps"
    metadata = ROOT / ".test-tools" / "frps.sha256"
    if target.exists() and metadata.exists():
        if hashlib.sha256(target.read_bytes()).hexdigest() == metadata.read_text().strip():
            print("测试内核摘要检查通过")
            return
    request = urllib.request.Request(
        f"https://api.github.com/repos/fatedier/frp/releases/tags/v{VERSION}",
        headers={"User-Agent": "frp-desktop-cli-check"},
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        release = json.load(response)
    name = f"frp_{VERSION}_darwin_{ARCH}.tar.gz"
    asset = next(x for x in release["assets"] if x["name"] == name)
    with urllib.request.urlopen(asset["browser_download_url"], timeout=60) as response:
        archive = response.read(80 * 1024 * 1024)
    digest = "sha256:" + hashlib.sha256(archive).hexdigest()
    if digest != asset["digest"]:
        raise RuntimeError("官方测试内核归档摘要不匹配")
    with tarfile.open(fileobj=io.BytesIO(archive)) as tar:
        member = next(x for x in tar.getmembers() if x.name.endswith("/frps"))
        if not member.isfile() or member.size > 80 * 1024 * 1024:
            raise RuntimeError("测试内核资产格式不正确")
        data = tar.extractfile(member).read()
    target.parent.mkdir(mode=0o700, exist_ok=True)
    target.write_bytes(data)
    target.chmod(0o700)
    metadata.write_text(hashlib.sha256(data).hexdigest() + "\n")
    print("已准备官方测试内核，仅用于本机 CLI 集成检查")


if __name__ == "__main__":
    main()
