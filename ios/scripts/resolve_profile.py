#!/usr/bin/env python3
"""Resolve iOS provisioning profile metadata by reference.

The script accepts a profile reference (name, UUID, filename, stem, or path),
searches common provisioning profile directories, and prints tab-separated
metadata for the best match:

name, uuid, path, cert_sha1, get_task_allow
"""

from __future__ import annotations

import glob
import os
import plistlib
import subprocess
import sys
import tempfile
from dataclasses import dataclass


@dataclass
class Profile:
    """Represents one decoded mobile provisioning profile."""

    name: str
    uuid: str
    path: str
    cert_sha1: str
    get_task_allow: bool


def _extract_cert_sha1(cert_der: bytes) -> str:
    """Return SHA1 fingerprint for a DER certificate as uppercase hex."""

    with tempfile.NamedTemporaryFile(delete=False) as handle:
        handle.write(cert_der)
        cert_path = handle.name
    try:
        output = subprocess.check_output(
            [
                "openssl",
                "x509",
                "-inform",
                "DER",
                "-in",
                cert_path,
                "-noout",
                "-fingerprint",
                "-sha1",
            ],
            stderr=subprocess.DEVNULL,
            text=True,
        )
    finally:
        os.unlink(cert_path)

    for line in output.splitlines():
        if "Fingerprint=" in line:
            return line.split("=", 1)[1].replace(":", "").strip()
    return ""


def _parse_profile(path: str) -> Profile:
    """Decode and parse one .mobileprovision file."""

    xml_bytes = subprocess.check_output(
        ["openssl", "smime", "-inform", "der", "-verify", "-noverify", "-in", path],
        stderr=subprocess.DEVNULL,
    )
    parsed = plistlib.loads(xml_bytes)
    certs = parsed.get("DeveloperCertificates") or []
    cert_sha1 = _extract_cert_sha1(certs[0]) if certs else ""
    entitlements = parsed.get("Entitlements") or {}

    return Profile(
        name=str(parsed.get("Name") or ""),
        uuid=str(parsed.get("UUID") or ""),
        path=path,
        cert_sha1=cert_sha1,
        get_task_allow=bool(entitlements.get("get-task-allow")),
    )


def _load_profiles() -> list[Profile]:
    """Load profiles from known directories, skipping unreadable entries."""

    search_dirs = [
        os.path.expanduser("~/Library/MobileDevice/Provisioning Profiles"),
        os.path.expanduser("~/Library/Developer/Xcode/UserData/Provisioning Profiles"),
    ]

    profiles: list[Profile] = []
    for directory in search_dirs:
        if not os.path.isdir(directory):
            continue
        for path in glob.glob(os.path.join(directory, "*.mobileprovision")):
            try:
                profiles.append(_parse_profile(path))
            except Exception:
                # Skip malformed or inaccessible profiles and continue.
                continue
    return profiles


def _find_matches(profiles: list[Profile], profile_ref: str) -> list[Profile]:
    """Return candidate matches for the requested reference."""

    expanded_ref = os.path.expanduser(profile_ref)
    matches: list[Profile] = []
    for profile in profiles:
        basename = os.path.basename(profile.path)
        stem, _ = os.path.splitext(basename)
        if os.path.isfile(expanded_ref):
            if os.path.realpath(profile.path) == os.path.realpath(expanded_ref):
                matches.append(profile)
            continue
        if profile_ref in (profile.name, profile.uuid, basename, stem):
            matches.append(profile)
    return matches


def main() -> int:
    """Entry point."""

    if len(sys.argv) != 2:
        print("usage: resolve_profile.py <profile-ref>", file=sys.stderr)
        return 2

    profile_ref = sys.argv[1].strip()
    if not profile_ref:
        print("profile reference is required", file=sys.stderr)
        return 2

    profiles = _load_profiles()
    if not profiles:
        print("No provisioning profiles found in expected directories.", file=sys.stderr)
        return 2

    matches = _find_matches(profiles, profile_ref)
    if not matches:
        print(f"Provisioning profile not found for reference: {profile_ref}", file=sys.stderr)
        print("Available profiles:", file=sys.stderr)
        for profile in sorted(profiles, key=lambda item: (item.name, item.uuid)):
            print(
                f"  - name={profile.name} uuid={profile.uuid} path={profile.path}",
                file=sys.stderr,
            )
        return 2

    # Prefer distribution-style profiles (get_task_allow=false), then newest.
    matches.sort(key=lambda item: (item.get_task_allow, -os.path.getmtime(item.path)))
    selected = matches[0]

    print(
        "\t".join(
            [
                selected.name,
                selected.uuid,
                selected.path,
                selected.cert_sha1,
                "true" if selected.get_task_allow else "false",
            ]
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
