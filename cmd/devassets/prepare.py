"""Prepare isolated development YAML and web assets, preserving HA storage."""
import hashlib
import json
import os
from pathlib import Path
import re
import sys

import yaml

WEB_EXTENSIONS = {".js", ".mjs", ".css", ".json", ".png", ".jpg", ".jpeg", ".svg", ".webp", ".gif", ".ico", ".woff", ".woff2", ".ttf"}


def root_keys(node, ancestors=frozenset()):
    """Inspect root and merged keys without constructing HA-specific tags."""
    if not isinstance(node, yaml.MappingNode):
        raise RuntimeError("configuration.yaml must contain a root mapping")
    if id(node) in ancestors:
        raise RuntimeError("configuration.yaml contains a recursive root merge")
    ancestors = ancestors | {id(node)}
    keys, direct = set(), set()
    for key, value in node.value:
        if not isinstance(key, yaml.ScalarNode):
            raise RuntimeError("configuration.yaml integration keys must be strings")
        if key.tag == "tag:yaml.org,2002:merge":
            merged = value.value if isinstance(value, yaml.SequenceNode) else [value]
            for mapping in merged:
                keys.update(root_keys(mapping, ancestors))
            continue
        if key.tag != "tag:yaml.org,2002:str":
            raise RuntimeError("configuration.yaml integration keys must be strings")
        if key.value in direct:
            raise RuntimeError("configuration.yaml contains duplicate root integration keys")
        direct.add(key.value)
        keys.add(key.value)
    return keys


def enable_development_api(text):
    # compose/serialize retain !include, !input and other HA tags without
    # resolving files or invoking YAML constructors. Only the runtime copy changes.
    try:
        node = yaml.compose(text, Loader=yaml.SafeLoader)
    except yaml.YAMLError as exc:
        raise RuntimeError("configuration.yaml is malformed YAML") from exc
    keys = root_keys(node)
    missing = [key for key in ("http", "api", "websocket_api", "frontend") if key not in keys]
    if not missing:
        return text
    for key in missing:
        node.value.append((yaml.ScalarNode("tag:yaml.org,2002:str", key),
                           yaml.ScalarNode("tag:yaml.org,2002:null", "null")))
    return yaml.serialize(node, Dumper=yaml.SafeDumper)


def prepare(source, runtime):
    source, runtime = Path(source), Path(runtime)
    manifest_path = runtime / ".denmother-source.json"
    static_manifest = runtime / ".denmother-static.json"
    previous = json.loads(manifest_path.read_text()) if manifest_path.exists() else {}
    old_static = json.loads(static_manifest.read_text()) if static_manifest.exists() else {}
    files, static = {}, {}
    for path in sorted(source.rglob("*")):
        relative = path.relative_to(source)
        if any(part.startswith(".") or part in {"tests", "custom_components"} for part in relative.parts):
            continue
        yaml_file = path.suffix in {".yaml", ".yml"}
        web_file = relative.parts[0] == "www" and path.suffix.lower() in WEB_EXTENSIONS
        if not (yaml_file or web_file) or not path.is_file():
            continue
        if not path.resolve().is_relative_to(source.resolve()):
            raise RuntimeError("Development assets must stay within their configuration directory")
        if "secrets" in path.name:
            raise RuntimeError("Use a dedicated development configuration without secrets files")
        contents = path.read_bytes()
        if yaml_file and re.search(rb"!secret\b", contents):
            raise RuntimeError("Portable development requires YAML without production secret references")
        target = runtime / relative
        if not target.resolve().is_relative_to(runtime.resolve()):
            raise RuntimeError("Development asset target must stay within the runtime directory")
        (files if yaml_file else static)[str(relative)] = hashlib.sha256(contents).hexdigest()
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(contents)
    for stale in (previous.keys() - files.keys()) | (old_static.keys() - static.keys()):
        target = runtime / stale
        if target.resolve().is_relative_to(runtime.resolve()):
            target.unlink(missing_ok=True)
    config = runtime / "configuration.yaml"
    config.write_text(enable_development_api(config.read_text()))
    manifest_path.write_text(json.dumps(files, sort_keys=True))
    static_manifest.write_text(json.dumps(static, sort_keys=True))


if __name__ == "__main__":
    prepare("/ha-config-source", "/config")
    os.execvp(sys.argv[1], sys.argv[1:])
