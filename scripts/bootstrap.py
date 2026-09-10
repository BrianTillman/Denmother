#!/usr/bin/env python3
"""Acquire and validate a dm executable; all registration policy lives in Go."""
import argparse
from datetime import datetime, timezone
import hashlib
import gzip
import io
import stat
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import struct
import subprocess
import sys
import tarfile
import tempfile
import urllib.error
import urllib.request
import uuid
import zlib

ROOT = Path(__file__).resolve().parent.parent
VERSION = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?\Z")
MAX_ARCHIVE = 512 * 1024 * 1024

class Failure(Exception):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code

class Parser(argparse.ArgumentParser):
    def error(self, message):
        raise Failure("invalid_arguments", "Invalid setup arguments; inspect ./setup --help for supported flags and values")

def parser():
    p = Parser(description=__doc__)
    source = p.add_mutually_exclusive_group()
    source.add_argument("--from-source", action="store_true")
    source.add_argument("--archive", type=Path)
    source.add_argument("--binary", type=Path)
    p.add_argument("--source", type=Path)
    p.add_argument("--version")
    p.add_argument("--checksum", type=Path)
    p.add_argument("--sha256")
    p.add_argument("--repository", default="BrianTillman/Denmother")
    p.add_argument("--bin-dir", "--directory")
    p.add_argument("--archive-dir", type=Path, help="Legacy local release directory; requires --version")
    p.add_argument("--harness", action="append", default=[])
    p.add_argument("--scope", choices=["user", "project"], default="user")
    p.add_argument("--project")
    p.add_argument("--destination")
    for flag in ("binary-only", "dry-run", "json", "offline", "recover"):
        p.add_argument("--" + flag, action="store_true")
    return p

def envelope(code, message, details=None, exit_code=1):
    now=datetime.now(timezone.utc).isoformat()
    return {"schema_version": "dm.operator.v1", "run_id": str(uuid.uuid4()),
            "command": "setup", "status": {0:"success",1:"failure",2:"warning",3:"partial"}[exit_code],
            "summary": message, "exit_code": exit_code, "started_at":now, "ended_at":now, "duration":"0s", "error_code":code,
            "steps": [{"id":"bootstrap", "title":"Bootstrap Denmother", "status":{0:"success",1:"failure",2:"warning",3:"partial"}[exit_code],
                       "summary":message, "error_code":code, "details": details or {}}]}

def host():
    system = {"Linux":"linux", "Darwin":"darwin"}.get(platform.system())
    arch = {"x86_64":"amd64", "amd64":"amd64", "aarch64":"arm64", "arm64":"arm64"}.get(platform.machine().lower())
    if not system or not arch:
        raise Failure("unsupported_platform", "Bootstrap supports Linux/macOS amd64/arm64; native Windows bootstrap is not validated.")
    return system, arch

class HTTPSRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if not newurl.startswith("https://"):
            raise Failure("integrity_failure", "Release redirect must use HTTPS")
        return super().redirect_request(req, fp, code, msg, headers, newurl)

def fetch(url, limit=MAX_ARCHIVE):
    if not url.startswith("https://"):
        raise Failure("invalid_arguments", "Release URLs must use HTTPS")
    request = urllib.request.Request(url, headers={"User-Agent":"Denmother-setup", "Accept":"application/vnd.github+json"})
    try:
        with urllib.request.build_opener(HTTPSRedirect).open(request, timeout=30) as response:
            data = response.read(limit + 1)
    except urllib.error.URLError as error:
        raise Failure("release_unavailable", "Release unavailable; use --from-source, --archive PATH, or a published --version: " + str(error)) from error
    if len(data)>limit:
        raise Failure("integrity_failure", "Release response exceeds size limit")
    return data

def expected_digest(archive, args):
    if args.sha256:
        value=args.sha256.lower()
    else:
        checksum=args.checksum or archive.parent / "SHA256SUMS"
        matches=[]
        for line in read_local(checksum,1 << 20).decode("utf-8").splitlines():
            parts=line.split()
            if len(parts)==2 and parts[1] in (archive.name,"*"+archive.name):
                matches.append(parts[0])
        if len(matches)!=1:
            raise Failure("checksum_failure", "Missing or duplicate archive checksum in " + str(checksum))
        value=matches[0].lower()
    if not re.fullmatch("[0-9a-f]{64}",value):
        raise Failure("checksum_failure", "Invalid expected SHA256 digest")
    return value

def verify_native(binary, system, arch):
    # Check executable headers before running version, including local archives.
    with binary.open("rb") as stream:
        header=stream.read(64)
    valid=False
    if system=="linux" and len(header)>=20 and header[:6]==b"\x7fELF\x02\x01":
        valid=struct.unpack_from("<H",header,18)[0]=={"amd64":62,"arm64":183}[arch]
    if system=="darwin" and len(header)>=8 and header[:4]==b"\xcf\xfa\xed\xfe":
        valid=struct.unpack_from("<I",header,4)[0]=={"amd64":0x1000007,"arm64":0x100000c}[arch]
    if not valid:
        raise Failure("unsupported_platform", "Payload executable does not match host " + system + "/" + arch)

def read_local(path, limit):
    # Opening nonblocking prevents a raced-in FIFO from hanging unattended setup.
    fd=os.open(path,os.O_RDONLY | getattr(os,"O_NONBLOCK",0))
    with os.fdopen(fd,"rb") as stream:
        info=os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode):
            raise Failure("integrity_failure", "Input must be a regular file")
        if info.st_size>limit:
            raise Failure("integrity_failure", "Input exceeds size limit")
        data=stream.read(limit+1)
    if len(data)>limit:
        raise Failure("integrity_failure", "Input exceeds size limit")
    return data

def unpack(archive, destination, args, system, arch):
    compressed=read_local(archive,MAX_ARCHIVE)
    actual=hashlib.sha256(compressed).hexdigest()
    if actual!=expected_digest(archive,args):
        raise Failure("checksum_failure", "Archive checksum mismatch; payload was not executed")
    allowed=set((ROOT / "scripts/release-assets.txt").read_text().splitlines()) | {"dm"}
    try:
        # Parse precisely the verified snapshot, never reopen an input path that
        # could have changed since verification. Bound gzip output before tar's
        # extended headers or sparse-file metadata can allocate memory.
        with gzip.GzipFile(fileobj=io.BytesIO(compressed)) as stream:
            expanded=stream.read(MAX_ARCHIVE+1)
        if len(expanded)>MAX_ARCHIVE:
            raise Failure("unsafe_archive", "Expanded archive exceeds size limit")
        with tarfile.open(fileobj=io.BytesIO(expanded), mode="r:") as package:
            seen=set()
            total=0
            for member in package:
                name=member.name
                if name not in allowed or name in seen or member.type not in (tarfile.REGTYPE,tarfile.AREGTYPE) or member.issparse() or str(PurePosixPath(name))!=name or "\\" in name:
                    raise Failure("unsafe_archive", "Unsafe or unexpected archive entry: " + name)
                seen.add(name)
                total+=member.size
                if member.size<0 or total>MAX_ARCHIVE:
                    raise Failure("unsafe_archive", "Expanded archive exceeds size limit")
            if "dm" not in seen:
                raise Failure("unsafe_archive", "Archive has no dm executable")
            with package.extractfile("dm") as stream:
                data=stream.read(MAX_ARCHIVE+1)
                if len(data)!=package.getmember("dm").size:
                    raise Failure("integrity_failure", "Truncated executable payload")
        binary=destination / "dm"
        # Staging destinations are private; never overwrite a pre-existing path.
        with binary.open("xb") as stream:
            stream.write(data)
    except (tarfile.TarError, EOFError, gzip.BadGzipFile, zlib.error, OverflowError) as error:
        raise Failure("integrity_failure", "Invalid release archive") from error
    verify_native(binary,system,arch)
    binary.chmod(0o755)
    return binary


def json_object(data):
    def unique(pairs):
        result={}
        for key,value in pairs:
            if key in result: raise ValueError("duplicate JSON key")
            result[key]=value
        return result
    value=json.loads(data,object_pairs_hook=unique)
    if not isinstance(value,dict): raise ValueError("expected JSON object")
    return value

def provenance(binary, version, system, arch):
    verify_native(binary,system,arch)
    try:
        result=subprocess.run([str(binary),"version","--json"], capture_output=True, text=True, timeout=30, check=True)
        doc=json_object(result.stdout)
        if doc.get("schema_version")!="dm.operator.v1" or doc.get("command")!="version" or doc.get("status")!="success" or doc.get("exit_code")!=0:
            raise ValueError("unsuccessful version envelope")
        steps=doc.get("steps")
        if not isinstance(steps,list) or not all(isinstance(s,dict) for s in steps):
            raise ValueError("invalid version steps")
        builds=[s for s in steps if s.get("id")=="build"]
        if len(builds)!=1 or not isinstance(builds[0].get("details"),dict):
            raise ValueError("missing or ambiguous build provenance")
        info=builds[0]["details"]
        if not isinstance(info.get("version"),str) or not info["version"]:
            raise ValueError("missing version")
        if not isinstance(info.get("revision","unknown"),str):
            raise ValueError("invalid revision")
    except (ValueError, TypeError, subprocess.SubprocessError) as error:
        raise Failure("incomplete_verification", "Payload version verification failed") from error
    if info.get("os")!=system or info.get("arch")!=arch:
        raise Failure("unsupported_platform", "Payload reported a different platform")
    if version and info["version"]!=version:
        raise Failure("version_mismatch", "Requested version does not match payload")
    return info

def forward(args):
    result=["setup", "--scope",args.scope]
    for flag in ("bin_dir","project","destination"):
        if getattr(args,flag) is not None:
            result += ["--"+flag.replace("_","-"),getattr(args,flag)]
    for h in args.harness:
        result += ["--harness",h]
    for flag in ("binary_only","dry_run","json","offline","recover"):
        if getattr(args,flag):
            result.append("--"+flag.replace("_","-"))
    return result

def delegate(binary, args, origin, revision=None):
    command=[str(binary),*forward(args),"--origin",origin]
    if revision is not None: command += ["--source-revision",revision]
    if not args.json:
        status=subprocess.run(command).returncode
        if status not in (0,1,2,3):
            raise Failure("incomplete_verification", "Installer process terminated without a complete result; inspect registration status before retrying")
        return status
    result=subprocess.run(command,capture_output=True,text=True)
    if result.stderr: print(result.stderr,end="",file=sys.stderr)
    try:
        doc=json_object(result.stdout)
        statuses={0:"success",1:"failure",2:"warning",3:"partial"}
        if result.returncode not in statuses or doc.get("exit_code")!=result.returncode or doc.get("status")!=statuses[result.returncode] or doc.get("schema_version")!="dm.operator.v1":
            raise ValueError("invalid installer result")
    except (ValueError,TypeError):
        report=envelope("incomplete_verification", "Installer process did not return a complete result; files may have changed, so inspect dm skills status before retrying",exit_code=3)
        print(json.dumps(report))
        return 3
    print(result.stdout,end="" if result.stdout.endswith("\n") else "\n")
    return result.returncode

def run(args):
    system,arch=host()
    if args.source is not None and not args.from_source:
        raise Failure("invalid_arguments", "--source requires --from-source")
    if args.source is None: args.source=ROOT
    if args.sha256 and args.checksum:
        raise Failure("invalid_arguments", "Select --sha256 or --checksum, not both")
    if (args.sha256 or args.checksum) and not (args.archive or args.archive_dir):
        raise Failure("invalid_arguments", "Checksum overrides require a local --archive or --archive-dir")
    if args.project is not None and args.scope!="project":
        raise Failure("invalid_arguments", "--project requires --scope project")
    if args.project is not None and not Path(args.project).is_dir():
        raise Failure("invalid_arguments", "--project must name an existing directory")
    if args.bin_dir is not None and not args.bin_dir.strip() or args.destination is not None and not args.destination.strip():
        raise Failure("invalid_arguments", "Destinations must not be empty")
    explicit={name.strip() for value in args.harness for name in value.split(",") if name.strip()!="detected"}
    if args.destination and len(explicit)>1:
        raise Failure("invalid_arguments", "--destination requires one harness")
    if args.version:
        args.version=args.version.removeprefix("v")
        if not VERSION.fullmatch(args.version):
            raise Failure("invalid_arguments", "Use an explicit semantic --version, for example 0.1.0-rc.1")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_][A-Za-z0-9_.-]*",args.repository):
        raise Failure("invalid_arguments", "Invalid GitHub OWNER/REPO")
    if args.archive_dir:
        if not args.version or args.archive or args.from_source or args.binary:
            raise Failure("invalid_arguments", "--archive-dir requires --version and cannot combine source modes")
        args.archive=args.archive_dir / f"denmother-{args.version}-{system}-{arch}.tar.gz"
    if args.from_source and args.version:
        raise Failure("invalid_arguments", "Source builds retain version dev; do not combine --from-source and --version")
    if args.scope=="project" and not args.project:
        raise Failure("invalid_arguments", "--scope project requires --project PATH")
    for value in args.harness:
        for name in value.split(","):
            if name.strip() not in ("detected","codex","claude"):
                raise Failure("invalid_arguments", "Unknown harness; supported: detected, codex, claude")
    if args.binary_only and (args.harness or args.destination or args.project or args.scope!="user"):
        raise Failure("invalid_arguments", "--binary-only cannot select harnesses or a skill destination")
    if args.archive and not args.archive.is_file():
        raise Failure("invalid_arguments", "Local archive does not exist: " + str(args.archive))
    if args.from_source and not (args.source / "go.mod").is_file():
        raise Failure("invalid_arguments", "--from-source requires a Denmother source checkout")
    if args.from_source:
        module=read_local(args.source/"go.mod",1 << 20).decode("utf-8").strip()
        if not module.startswith("module github.com/BrianTillman/Denmother\n"):
            raise Failure("invalid_arguments", "Source must be the Denmother Go module")
    # Unpacked releases use their companion binary. In a checkout, omitting input
    # flags selects a published release; building requires --from-source.
    if not any((args.binary,args.archive,args.from_source,args.version)) and (ROOT/"dm").is_file() and not (ROOT/"main.go").exists():
        args.binary=ROOT/"dm"
    origin="source" if args.from_source else "archive" if args.archive else "existing-binary" if args.binary else "download"
    if args.offline and origin=="download":
        raise Failure("offline_input_required", "Offline setup requires --binary PATH, --archive PATH, or --from-source with cached Go dependencies")
    if args.dry_run:
        if args.binary:
            binary=args.binary.resolve()
            provenance(binary,args.version,system,arch)
            return delegate(binary,args,origin)
        # Destination planning needs a matching executable. Defer it during dry-run
        # rather than building a planner or creating temporary files.
        doc=envelope("incomplete_verification", "Bootstrap plan; full destination and payload verification require an available dm binary", {"plan_id":str(uuid.uuid4()),"requested_version":args.version,"resolved_version":None,"origin":origin,"platform":system+"/"+arch,"operations":[{"action":"obtain_payload","state":"planned"},{"action":"plan_and_install_with_dm","state":"planned"}],"verification":"deferred; use --binary PATH --dry-run for a complete local plan"},3)
        print(json.dumps(doc) if args.json else doc["summary"])
        return 3
    with tempfile.TemporaryDirectory(prefix="denmother-bootstrap-") as temp:
        stage=Path(temp)
        revision="unknown"
        if args.from_source:
            environment=os.environ.copy()
            if args.offline:
                environment.update(GOPROXY="off", GOSUMDB="off", GOTOOLCHAIN="local")
            source=args.source.resolve()
            try:
                revision=subprocess.check_output(["git","-C",str(source),"rev-parse","HEAD"],stderr=subprocess.DEVNULL,text=True).strip()
            except (subprocess.CalledProcessError, FileNotFoundError):
                pass
            try:
                dirty=subprocess.check_output(["git","-C",str(source),"status","--porcelain"],stderr=subprocess.DEVNULL,text=True)
                if dirty: revision+="-dirty"
            except (subprocess.CalledProcessError, FileNotFoundError):
                revision+="-unavailable"
            binary=stage/"dm"
            subprocess.run(["go","build","-buildvcs=false","-ldflags","-X github.com/BrianTillman/Denmother/cmd.revision="+revision,"-o",str(binary),"."],cwd=source,env=environment,stdout=sys.stderr,stderr=sys.stderr,check=True)
        elif args.binary:
            binary=args.binary.resolve()
        else:
            archive=args.archive
            if archive is None:
                if not args.version:
                    try:
                        release=json_object(fetch("https://api.github.com/repos/"+args.repository+"/releases/latest",1 << 20))
                        tag=release.get("tag_name")
                        if not isinstance(tag,str): raise ValueError("missing release tag")
                    except ValueError as error:
                        raise Failure("release_unavailable", "Release lookup returned invalid metadata") from error
                    args.version=tag.removeprefix("v")
                    if not VERSION.fullmatch(args.version) or "-" in args.version:
                        raise Failure("version_mismatch", "Latest release did not resolve to a stable semantic version")
                name=f"denmother-{args.version}-{system}-{arch}.tar.gz"
                base=f"https://github.com/{args.repository}/releases/download/v{args.version}/"
                archive=stage/name
                archive.write_bytes(fetch(base+name))
                (stage/"SHA256SUMS").write_bytes(fetch(base+"SHA256SUMS",1 << 20))
            # Local filenames encode release identity; explicit digest does not
            # waive matching host or version validation.
            match=re.fullmatch(r"denmother-(.+)-(linux|darwin)-(amd64|arm64)\.tar\.gz",archive.name)
            if not match or not VERSION.fullmatch(match[1]):
                raise Failure("invalid_arguments", "Archive must use denmother-VERSION-OS-ARCH.tar.gz naming")
            if (match[2],match[3])!=(system,arch):
                raise Failure("unsupported_platform", "Archive filename does not match host platform")
            if args.version and match[1]!=args.version:
                raise Failure("version_mismatch", "Archive filename does not match requested version")
            args.version=match[1]
            binary=unpack(archive,stage,args,system,arch)
        info=provenance(binary,args.version,system,arch)
        if args.from_source and info["version"]!="dev":
            raise Failure("version_mismatch", "Source build must retain development provenance")
        return delegate(binary,args,origin,revision if args.from_source else info.get("revision","unknown"))

def main():
    json_mode=any(a in ("--json","--json=true") for a in sys.argv[1:])
    try:
        return run(parser().parse_args())
    except KeyboardInterrupt:
        doc=envelope("incomplete_verification", "Setup interrupted; inspect registration status and any recovery journal before retrying",exit_code=3)
        print(json.dumps(doc) if json_mode else doc["summary"],file=sys.stdout if json_mode else sys.stderr)
        return 3
    except (Failure,OSError,subprocess.SubprocessError,ValueError,KeyError) as error:
        code=getattr(error,"code","permission_failure" if isinstance(error,PermissionError) else "bootstrap_failed")
        doc=envelope(code,str(error))
        print(json.dumps(doc) if json_mode else str(error),file=sys.stdout if json_mode else sys.stderr)
        return 1

if __name__=="__main__":
    sys.exit(main())
