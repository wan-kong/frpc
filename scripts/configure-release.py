#!/usr/bin/env python3
"""Validate or write public release identity. Never builds, signs, or publishes."""

import argparse
import base64
import json
import os
import pathlib
import re

ROOT = pathlib.Path(__file__).resolve().parent.parent


def validate(config):
    bundle = config.get("identifier", "")
    if not re.fullmatch(r"[A-Za-z][A-Za-z0-9-]*(\.[A-Za-z0-9-]+){2,}", bundle):
        raise ValueError("需要合法的最终 Bundle ID")
    if bundle.startswith("top.wankong."):
        raise ValueError("示例 Bundle ID 不能用于正式发布")
    updates = config.get("updates", {})
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", updates.get("github", "")):
        raise ValueError("需要 GitHub 发布仓库 owner/repo")
    key = base64.b64decode(updates.get("publicKey", ""), validate=True)
    if len(key) != 32:
        raise ValueError("需要 mygo keygen 产生的 Ed25519 公钥")
    if not re.fullmatch(r"\d+\.\d+\.\d+", config.get("version", "")):
        raise ValueError("版本必须使用稳定版 semver")
    mac = config.get("macos", {})
    if not mac.get("signingIdentity", "").startswith("Developer ID Application:"):
        raise ValueError("需要 Developer ID Application 签名身份")
    if not mac.get("notarize", {}).get("keychainProfile"):
        raise ValueError("需要公证 Keychain profile")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--write",
        action="store_true",
        help="使用环境变量写入 mygo.json 中的公开发行配置",
    )
    args = parser.parse_args()
    path = ROOT / "mygo.json"
    config = json.loads(path.read_text())
    if args.write:
        config["identifier"] = os.environ["FRP_BUNDLE_ID"]
        config["updates"] = {
            "github": os.environ["FRP_RELEASE_REPO"],
            "publicKey": os.environ["FRP_UPDATE_PUBLIC_KEY"].strip(),
        }
        config["macos"]["signingIdentity"] = os.environ["FRP_SIGNING_IDENTITY"]
        config["macos"]["notarize"] = {
            "keychainProfile": os.environ["FRP_NOTARY_PROFILE"]
        }
    validate(config)
    if args.write:
        temporary = path.with_suffix(".json.pending")
        temporary.write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n")
        temporary.replace(path)
    print("发行配置检查通过；未执行打包、签名、公证或发布")


if __name__ == "__main__":
    main()
