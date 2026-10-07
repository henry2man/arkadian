package store

const cacheHelper = `
import datetime, fcntl, hashlib, json, os, pathlib, socket, stat, subprocess, sys, tempfile

action, location = sys.argv[1:]
root = pathlib.Path(location).absolute()

def emit(value):
    print(json.dumps(value, sort_keys=True))

def identity():
    if root.exists():
        info = root.stat()
        return socket.gethostname() + ":" + str(info.st_dev) + ":" + str(info.st_ino)
    return socket.gethostname() + ":" + str(root.resolve())

def payload(hashing=False):
    real = root.resolve(strict=True)
    base = os.path.normpath(str(root))  # one-hop link test, before the symlink chain resolves
    shared = (root.parent / "blobs").resolve()  # hf 2.x shared blob store, sibling of the repository
    snapshots = root / "snapshots"
    if not snapshots.is_dir() or snapshots.is_symlink():
        raise ValueError("not a native HF model cache: " + str(root))
    revisions = sorted(entry.name for entry in snapshots.iterdir() if entry.name != ".DS_Store")
    if not revisions or any(len(revision) != 40 or any(ch not in "0123456789abcdef" for ch in revision) for revision in revisions):
        raise ValueError("missing or invalid HF revisions")
    files, links, stamps = {}, {}, {}
    for current, directories, filenames in os.walk(root, followlinks=False):
        directories[:] = sorted(name for name in directories if name != ".locks")
        for name in directories:
            if (pathlib.Path(current) / name).is_symlink():
                raise ValueError("directory symlinks inside a repository are not supported")
        for name in sorted(filenames):
            path = pathlib.Path(current) / name
            relative = str(path.relative_to(root))
            if relative == ".arkmeta.json" or name == ".DS_Store" or name.endswith((".incomplete", ".lock", ".tmp")):
                continue
            resolved = path.resolve(strict=True)
            if not resolved.is_relative_to(real) and not resolved.is_relative_to(shared):
                raise ValueError("file points outside its repository: " + relative)
            info = path.stat()
            if not stat.S_ISREG(info.st_mode):
                raise ValueError("not a regular file: " + relative)
            shared_link = False
            if path.is_symlink():
                target = os.readlink(path)
                if os.path.isabs(target):
                    raise ValueError("absolute snapshot links cannot be transported: " + relative)
                first = os.path.normpath(os.path.join(current, target))
                if first == base or first.startswith(base + os.sep):
                    links[relative] = target
                else:
                    shared_link = True  # hf 2.x shared blob: the transfer writes real bytes
            if not path.is_symlink() or shared_link:
                entry = {"size": info.st_size}
                if hashing:
                    digest = hashlib.sha256()
                    with path.open("rb") as source:
                        for chunk in iter(lambda: source.read(8 * 1024 * 1024), b""):
                            digest.update(chunk)
                    after = path.stat()
                    if (info.st_size, info.st_mtime_ns) != (after.st_size, after.st_mtime_ns):
                        raise ValueError("source changed while hashing: " + relative)
                    entry["sha256"] = digest.hexdigest()
                files[relative] = entry
            stamps[relative] = [info.st_size, info.st_mtime_ns, links.get(relative, "")]
    return {"revisions": revisions, "files": files, "links": links}, stamps

def manifest():
    result, _ = payload(True)
    digest = hashlib.sha256(json.dumps(result, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    result["digest"] = digest
    return result

def save(value):
    if root.is_symlink():
        raise ValueError("cannot write a manifest through a repository reference")
    _, stamps = payload()
    value["verified_fingerprint"] = hashlib.sha256(json.dumps(stamps, sort_keys=True).encode()).hexdigest()
    value["verified_at"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
    descriptor, temporary = tempfile.mkstemp(prefix=".arkmeta-", suffix=".tmp", dir=root)
    try:
        with os.fdopen(descriptor, "w") as stream:
            json.dump(value, stream, sort_keys=True)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, root / ".arkmeta.json")
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)

if action == "prepare":
    if (root / "models").is_dir():
        raise ValueError("old Arkadian layout detected; register a native HF cache instead")
    root.mkdir(parents=True, exist_ok=True)
    emit(None)
elif action == "mkdir":
    root.mkdir(parents=True, exist_ok=True)
    emit(None)
elif action == "catalog":
    if not root.is_dir():
        raise ValueError("vault is unavailable; mount its folder or run pull for a new cache")
    if (root / "models").is_dir():
        raise ValueError("old Arkadian layout is not a native HF cache")
    emit(sorted(entry.name for entry in root.iterdir() if entry.name.startswith("models--")))
elif action == "identity":
    emit(identity())
elif action == "inspect":
    if not os.path.lexists(root):
        emit(None)
    else:
        result, stamps = payload()
        revisions = []
        for revision in result["revisions"]:
            prefix = "snapshots/" + revision + "/"
            size = sum(value[0] for path, value in stamps.items() if path.startswith(prefix))
            refs = []
            if (root / "refs").exists():
                for path in (root / "refs").rglob("*"):
                    if path.is_file() and path.read_text().strip() == revision:
                        refs.append(str(path.relative_to(root / "refs")))
            revisions.append({"revision": revision, "refs": sorted(refs), "size_bytes": size})
        emit({"path": str(root), "identity": identity(), "reference": root.is_symlink(),
              "fingerprint": hashlib.sha256(json.dumps(stamps, sort_keys=True).encode()).hexdigest(),
              "size_bytes": sum(value["size"] for value in result["files"].values()), "revisions": revisions})
elif action == "hash":
    emit(manifest())
elif action == "meta":
    path = root / ".arkmeta.json"
    emit(json.loads(path.read_text()) if path.exists() else None)
elif action == "save":
    save(json.load(sys.stdin))
    emit(None)
elif action == "publish":
    publication = json.load(sys.stdin)
    staging = pathlib.Path(publication["staging"])
    lock_dir = root.parent / ".locks" / "ark-publish"
    lock_dir.mkdir(parents=True, exist_ok=True)
    with (lock_dir / root.name).open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if root.is_symlink() and publication.get("reference_identity") == identity():
            root.unlink()
        elif os.path.lexists(root):
            raise ValueError("destination appeared during transfer; source retained")
        os.rename(staging, root)
    emit(None)
elif action == "delete":
    expected = json.load(sys.stdin)
    if root.is_symlink():
        root.unlink()
    else:
        locks = []
        try:
            lock_dir = root.parent / ".locks" / root.name
            if lock_dir.exists():
                for path in sorted(lock_dir.glob("*.lock")):
                    lock = path.open("a")
                    locks.append(lock)
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            if expected is not None and manifest()["digest"] != expected["digest"]:
                raise ValueError("source changed; deletion refused")
            repo = root.name.removeprefix("models--").replace("--", "/")
            result = subprocess.run(["hf", "cache", "rm", "model/" + repo, "--cache-dir", str(root.parent), "--yes"], stdout=sys.stderr)
            if result.returncode or os.path.lexists(root):
                raise ValueError("HF cache deletion failed; inspect the source and run refresh")
        finally:
            for lock in locks:
                lock.close()
    emit(None)
else:
    raise ValueError("unknown cache operation")
`
