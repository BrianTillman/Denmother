import argparse
import hashlib
import io
import json
import functools
import http.server
import ssl
import subprocess
import threading
import shutil
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import bootstrap as b

class BootstrapTests(unittest.TestCase):
    def test_archive_safety_and_checksum(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp)
            archive=root/"denmother-1.2.3-linux-amd64.tar.gz"
            cases=[("../escape",tarfile.REGTYPE),("/absolute",tarfile.REGTYPE),("dm",tarfile.SYMTYPE),("dm",tarfile.LNKTYPE),("dm",tarfile.FIFOTYPE),("unexpected",tarfile.REGTYPE)]
            for name,kind in cases:
                with self.subTest(name=name,kind=kind):
                    with tarfile.open(archive,"w:gz") as output:
                        info=tarfile.TarInfo(name);info.type=kind;info.linkname="/tmp/foreign"
                        output.addfile(info)
                    args=argparse.Namespace(sha256=hashlib.sha256(archive.read_bytes()).hexdigest(),checksum=None)
                    with self.assertRaisesRegex(b.Failure,"Unsafe"):
                        b.unpack(archive,root,args,"linux","amd64")
            args.sha256="0"*64
            with self.assertRaisesRegex(b.Failure,"checksum mismatch"):
                b.unpack(archive,root,args,"linux","amd64")
            with tarfile.open(archive,"w:gz") as output:
                for unused in range(2):
                    output.addfile(tarfile.TarInfo("dm"))
            args.sha256=hashlib.sha256(archive.read_bytes()).hexdigest()
            with self.assertRaisesRegex(b.Failure,"Unsafe"):
                b.unpack(archive,root,args,"linux","amd64")

    def test_wrong_platform_never_executes(self):
        with tempfile.TemporaryDirectory() as temp:
            binary=Path(temp)/"dm";binary.write_bytes(b"#!/bin/sh\ntouch /tmp/must-not-execute\n")
            with patch.object(b.subprocess,"run",side_effect=AssertionError("executed")):
                with self.assertRaisesRegex(b.Failure,"does not match host"):
                    b.provenance(binary,"1.0.0","linux","amd64")

    def test_wrong_version(self):
        result=argparse.Namespace(stdout=json.dumps({"schema_version":"dm.operator.v1","command":"version","status":"success","exit_code":0,"steps":[{"id":"build","details":{"version":"2.0.0","os":"linux","arch":"amd64"}}]}))
        with patch.object(b,"verify_native"),patch.object(b.subprocess,"run",return_value=result):
            with self.assertRaisesRegex(b.Failure,"Requested version"):
                b.provenance(Path("dm"),"1.0.0","linux","amd64")

    def test_offline_and_dry_run_no_acquisition(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp)
            with patch.object(b,"ROOT",root),patch.object(b,"fetch",side_effect=AssertionError("network")),patch.object(b.subprocess,"run",side_effect=AssertionError("build")),patch.object(b.tempfile,"TemporaryDirectory",side_effect=AssertionError("write")),patch("sys.stdout",new_callable=io.StringIO):
                with self.assertRaisesRegex(b.Failure,"Offline setup requires"):
                    b.run(b.parser().parse_args(["--offline"]))
                self.assertEqual(b.run(b.parser().parse_args(["--dry-run","--version","1.0.0","--json"])),3)
            self.assertEqual(list(root.iterdir()),[])

    def test_latest_release_resolves_concrete_identity(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);urls=[]
            def fetch(url,limit=b.MAX_ARCHIVE):
                urls.append(url)
                if url.endswith("/latest"):
                    return b'{"tag_name":"v1.2.3"}'
                return b"fixture"
            def unpack(archive,stage,args,system,arch):
                self.assertEqual(args.version,"1.2.3")
                return stage/"dm"
            with patch.object(b,"ROOT",root),patch.object(b,"fetch",side_effect=fetch),patch.object(b,"unpack",side_effect=unpack),patch.object(b,"provenance",return_value={"version":"1.2.3"}),patch.object(b.subprocess,"run",return_value=argparse.Namespace(returncode=0)):
                self.assertEqual(b.run(b.parser().parse_args([])),0)
            self.assertEqual(len(urls),3)
            self.assertIn("/releases/download/v1.2.3/denmother-1.2.3-",urls[1])

    def test_local_release_server(self):
        if not shutil.which("openssl"):
            self.skipTest("openssl required for local HTTPS release fixture")
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp)
            subprocess.run(["openssl","req","-x509","-newkey","rsa:2048","-nodes","-keyout",str(root/"key.pem"),"-out",str(root/"cert.pem"),"-days","1","-subj","/CN=localhost","-addext","subjectAltName=DNS:localhost"],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,check=True)
            class QuietHandler(http.server.SimpleHTTPRequestHandler):
                def log_message(self,*args):
                    pass
            handler=functools.partial(QuietHandler,directory=str(root))
            server=http.server.ThreadingHTTPServer(("127.0.0.1",0),handler)
            context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.load_cert_chain(str(root/"cert.pem"),str(root/"key.pem"))
            server.socket=context.wrap_socket(server.socket,server_side=True)
            thread=threading.Thread(target=server.serve_forever,daemon=True)
            thread.start()
            try:
                archive=root/"denmother-1.0.0-linux-amd64.tar.gz"
                binary=bytearray(64);binary[:6]=b"\x7fELF\x02\x01";binary[18]=62
                with tarfile.open(archive,"w:gz") as output:
                    info=tarfile.TarInfo("dm");info.size=len(binary);output.addfile(info,io.BytesIO(binary))
                checksum=hashlib.sha256(archive.read_bytes()).hexdigest()
                (root/"SHA256SUMS").write_text(checksum+"  "+archive.name+"\n")
                base="https://localhost:"+str(server.server_port)+"/"
                download=root/"download";download.mkdir()
                with patch.dict(os.environ,{"SSL_CERT_FILE":str(root/"cert.pem")}):
                    (download/archive.name).write_bytes(b.fetch(base+archive.name))
                    (download/"SHA256SUMS").write_bytes(b.fetch(base+"SHA256SUMS"))
                args=argparse.Namespace(sha256=None,checksum=None)
                result=b.unpack(download/archive.name,download,args,"linux","amd64")
                self.assertEqual(result.read_bytes(),bytes(binary))
            finally:
                server.shutdown();server.server_close();thread.join()

    def test_https_required(self):
        with self.assertRaisesRegex(b.Failure,"HTTPS"):
            b.fetch("http://example.test/archive")
        with self.assertRaisesRegex(b.Failure,"HTTPS"):
            b.HTTPSRedirect().redirect_request(None,None,302,"",{},"http://example.test/archive")

class BootstrapHardeningTests(unittest.TestCase):
    def archive(self, root, payload=None, sparse=False):
        archive=root/"denmother-1.2.3-linux-amd64.tar.gz"
        if payload is None:
            payload=bytearray(64);payload[:6]=b"\x7fELF\x02\x01";payload[18]=62
        with tarfile.open(archive,"w:gz") as output:
            entry=tarfile.TarInfo("dm");entry.size=len(payload)
            if sparse: entry.type=tarfile.GNUTYPE_SPARSE
            output.addfile(entry,io.BytesIO(payload))
        args=argparse.Namespace(sha256=hashlib.sha256(archive.read_bytes()).hexdigest(),checksum=None)
        return archive,args,bytes(payload)

    def test_archive_replacement_cannot_bypass_checksum(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);archive,args,original=self.archive(root)
            expected=args.sha256
            def replace_input(unused_archive,unused_args):
                self.archive(root,b"malicious replacement")
                return expected
            with patch.object(b,"expected_digest",side_effect=replace_input):
                result=b.unpack(archive,root,args,"linux","amd64")
            self.assertEqual(result.read_bytes(),original)

    def test_decompression_is_bounded_before_tar_parsing(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);archive,args,_=self.archive(root)
            # tarfile pads this fixture to a 10 KiB record. The 4 KiB limit allows
            # the compressed input but rejects its expanded form before parsing.
            with patch.object(b,"MAX_ARCHIVE",4096),patch.object(b.tarfile,"open",side_effect=AssertionError("unbounded tar parsing")):
                with self.assertRaisesRegex(b.Failure,"Expanded archive"):
                    b.unpack(archive,root,args,"linux","amd64")
            self.assertFalse((root/"dm").exists())

    def test_sparse_archive_and_existing_stage_are_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);archive,args,_=self.archive(root,sparse=True)
            with self.assertRaises(b.Failure): b.unpack(archive,root,args,"linux","amd64")
            archive,args,_=self.archive(root)
            stage=root/"dm";stage.write_bytes(b"preserve this")
            with self.assertRaises(FileExistsError): b.unpack(archive,root,args,"linux","amd64")
            self.assertEqual(stage.read_bytes(),b"preserve this")

    def test_malformed_version_outputs_are_structured_failures(self):
        cases=['null','[]','{"steps":null}','{"schema_version":"dm.operator.v1","schema_version":"duplicate"}']
        base={"schema_version":"dm.operator.v1","command":"version","status":"success","exit_code":0,"steps":[{"id":"build","details":{"version":"1.0.0","os":"linux","arch":"amd64"}}]}
        for steps in [None,[None],[{"id":"build","details":[]}],base["steps"]*2]:
            doc=dict(base);doc["steps"]=steps;cases.append(json.dumps(doc))
        for value in cases:
            with self.subTest(value=value),patch.object(b,"verify_native"),patch.object(b.subprocess,"run",return_value=argparse.Namespace(stdout=value)):
                with self.assertRaises(b.Failure) as failure: b.provenance(Path("dm"),"1.0.0","linux","amd64")
                self.assertEqual(failure.exception.code,"incomplete_verification")

    def test_bad_flags_fail_without_acquisition(self):
        cases=[["--binary","dm","--checksum","checksums"],["--source","."],["--bin-dir",""],["--destination",""],["--scope","user","--project","."],["--destination","skill","--harness","codex,claude"],["--archive","archive","--sha256","a","--checksum","checksums"],["--binary-only","--scope","project","--project","."]]
        for flags in cases:
            with self.subTest(flags=flags),patch.object(b,"fetch",side_effect=AssertionError("network")),patch.object(b.tempfile,"TemporaryDirectory",side_effect=AssertionError("writes")),patch.object(b.subprocess,"run",side_effect=AssertionError("execution")):
                with self.assertRaises(b.Failure) as failure: b.run(b.parser().parse_args(flags))
                self.assertEqual(failure.exception.code,"invalid_arguments")

    def test_crashed_or_malformed_delegate_emits_one_envelope(self):
        args=b.parser().parse_args(["--binary","dm","--json"])
        for status,output in [(-9,""),(0,"not JSON"),(0,'{"status":"failure"}'),(7,'{}')]:
            with patch.object(b.subprocess,"run",return_value=argparse.Namespace(returncode=status,stdout=output,stderr="")),patch("sys.stdout",new_callable=io.StringIO) as stream:
                self.assertEqual(b.delegate(Path("dm"),args,"existing-binary"),3)
                result=json.loads(stream.getvalue())
                self.assertEqual(result["exit_code"],3)
                self.assertEqual(result["error_code"],"incomplete_verification")

    def test_offline_source_build_does_not_require_git(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp)
            (root/"go.mod").write_text("module github.com/BrianTillman/Denmother\n\ngo 1.26.8\n")
            args=b.parser().parse_args(["--from-source","--source",str(root),"--offline","--binary-only"])
            with patch.object(b.subprocess,"check_output",side_effect=FileNotFoundError),patch.object(b.subprocess,"run",return_value=argparse.Namespace(returncode=0)) as build,patch.object(b,"provenance",return_value={"version":"dev"}),patch.object(b,"delegate",return_value=0) as delegate,patch.object(b,"fetch",side_effect=AssertionError("network")):
                self.assertEqual(b.run(args),0)
                environment=build.call_args.kwargs["env"]
                self.assertEqual(environment["GOPROXY"],"off")
                self.assertEqual(environment["GOSUMDB"],"off")
                self.assertEqual(environment["GOTOOLCHAIN"],"local")
                self.assertEqual(delegate.call_args.args[3],"unknown-unavailable")

    def test_keyboard_interrupt_retains_json_exit_contract(self):
        with patch.object(b,"run",side_effect=KeyboardInterrupt),patch.object(b.sys,"argv",["setup","--json"]),patch("sys.stdout",new_callable=io.StringIO) as stream:
            self.assertEqual(b.main(),3)
            result=json.loads(stream.getvalue())
            self.assertEqual(result["exit_code"],3)
            self.assertEqual(result["error_code"],"incomplete_verification")

    def test_duplicate_missing_and_oversized_checksums_fail(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);archive,args,_=self.archive(root)
            checksum=args.sha256;args.sha256=None
            for content in ["", f"{checksum}  {archive.name}\n"*2,f"{checksum}  **{archive.name}\n", "x"*((1 << 20)+1)]:
                (root/"SHA256SUMS").write_text(content)
                with self.assertRaises(b.Failure): b.expected_digest(archive,args)

    def test_invalid_latest_release_metadata_is_structured(self):
        for value in [b'null',b'[]',b'{"tag_name":1}',b'{"tag_name":null}',b'{"tag_name":"v1.0.0","tag_name":"v9.0.0"}']:
            with tempfile.TemporaryDirectory() as temp,patch.object(b,"ROOT",Path(temp)),patch.object(b,"fetch",return_value=value):
                with self.assertRaises(b.Failure) as failure: b.run(b.parser().parse_args([]))
                self.assertEqual(failure.exception.code,"release_unavailable")

if __name__=="__main__":
    unittest.main()
