"""Immutable, verifiable node snapshots. SQLite uses the online backup API.

Cross-database consistency requires stopped writers; online snapshots explicitly
record that they are only consistent per database. No live WAL files are copied.
"""
from contextlib import closing
import argparse
import hashlib
import json
import os
import shutil
import sqlite3
import stat
import time
import uuid
from pathlib import Path, PurePosixPath


def safe_relative(value):
    path = PurePosixPath(value)
    if not value or path.is_absolute() or '..' in path.parts or '\\' in value or ':' in value or str(path) != value:
        raise ValueError('unsafe snapshot path')
    return path


def digest(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for data in iter(lambda: stream.read(1024 * 1024), b''):
            h.update(data)
    return h.hexdigest()


def copy_consistent(source, target):
    target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with source.open('rb') as stream:
        is_database = stream.read(16) == b'SQLite format 3\x00'
    if is_database:
        deadline = time.monotonic() + 120
        def progress(status, remaining, total):
            if time.monotonic() > deadline:
                raise TimeoutError('database backup deadline exceeded')
        with closing(sqlite3.connect(source.as_uri() + '?mode=ro', uri=True)) as src, closing(sqlite3.connect(target)) as dst:
            src.backup(dst, pages=256, progress=progress, sleep=0.05)
            if dst.execute('PRAGMA quick_check').fetchone()[0] != 'ok':
                raise ValueError('database integrity failure')
    else:
        for attempt in range(3):
            before = source.stat()
            shutil.copyfile(source, target)
            after = source.stat()
            if (before.st_size, before.st_mtime_ns) == (after.st_size, after.st_mtime_ns):
                break
        else:
            raise ValueError('file changed during snapshot')
    os.chmod(target, 0o600)
    with target.open('r+b') as stream:
        os.fsync(stream.fileno())
    return 'sqlite' if is_database else 'file'


def create_snapshot(node, sources, destination, quiescent=False):
    if not node or len(node) > 80:
        raise ValueError('invalid node')
    destination = Path(destination).resolve()
    if destination.exists():
        raise FileExistsError('snapshot destination must be new')
    for label, root in sources.items():
        safe_relative(label)
        root = Path(root).resolve()
        if destination == root or root in destination.parents:
            raise ValueError('snapshot destination is inside source')
    stage = destination.with_name(destination.name + '.partial-' + uuid.uuid4().hex)
    stage.mkdir(parents=True, mode=0o700)
    records = []
    try:
        for label, raw in sources.items():
            root = Path(raw).absolute()
            if root.is_symlink() or not root.exists():
                raise ValueError('source missing or linked: ' + label)
            entries = sorted(root.rglob('*')) if root.is_dir() else [root]
            for source in entries:
                if source.is_symlink():
                    raise ValueError('symbolic links are not snapshot inputs')
                if source.is_dir():
                    continue
                if not stat.S_ISREG(source.stat().st_mode):
                    raise ValueError('non-regular snapshot input')
                if source.name.endswith(('-wal', '-shm')):
                    base = source.with_name(source.name.rsplit('-', 1)[0])
                    if base.is_file():
                        with base.open('rb') as stream:
                            if stream.read(16) == b'SQLite format 3\x00':
                                continue
                relative = str(PurePosixPath(label) / (source.relative_to(root).as_posix() if root.is_dir() else source.name))
                safe_relative(relative)
                target = stage / 'data' / relative
                kind = copy_consistent(source, target)
                records.append({'path': relative, 'kind': kind, 'bytes': target.stat().st_size, 'sha256': digest(target)})
        manifest = {'version': 1, 'node': node, 'created_unix': time.time(), 'consistency': 'quiescent' if quiescent else 'per-database-online', 'files': records}
        with (stage / 'manifest.json').open('x', encoding='utf-8') as stream:
            json.dump(manifest, stream, ensure_ascii=False, indent=2)
            stream.flush(); os.fsync(stream.fileno())
        verify_snapshot(stage)
        os.rename(stage, destination)
        return manifest
    except Exception:
        # Retain incomplete evidence, never publish it as a usable snapshot.
        raise


def verify_snapshot(root):
    root = Path(root).resolve()
    manifest = json.loads((root / 'manifest.json').read_text(encoding='utf-8'))
    if manifest.get('version') != 1 or not isinstance(manifest.get('files'), list):
        raise ValueError('unsupported manifest')
    seen = set()
    for record in manifest['files']:
        relative = safe_relative(record['path'])
        if str(relative) in seen:
            raise ValueError('duplicate snapshot path')
        seen.add(str(relative))
        path = root / 'data' / relative
        if path.is_symlink() or not path.is_file() or not path.resolve().is_relative_to(root / 'data'):
            raise ValueError('snapshot path escaped')
        if path.stat().st_size != record['bytes'] or digest(path) != record['sha256']:
            raise ValueError('snapshot checksum mismatch')
        if record['kind'] == 'sqlite':
            with closing(sqlite3.connect(path.as_uri() + '?mode=ro', uri=True)) as db:
                if db.execute('PRAGMA quick_check').fetchone()[0] != 'ok':
                    raise ValueError('snapshot database corrupt')
    return manifest


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest='action', required=True)
    create = sub.add_parser('create')
    create.add_argument('--config', required=True)
    create.add_argument('--destination', required=True)
    create.add_argument('--quiescent', action='store_true', help='Only use after all writers are stopped')
    verify = sub.add_parser('verify'); verify.add_argument('snapshot')
    args = parser.parse_args()
    if args.action == 'create':
        cfg = json.loads(Path(args.config).read_text(encoding='utf-8'))
        result = create_snapshot(cfg['node'], cfg['sources'], args.destination, args.quiescent)
    else:
        result = verify_snapshot(args.snapshot)
    print(json.dumps({'node': result['node'], 'files': len(result['files']), 'bytes': sum(x['bytes'] for x in result['files']), 'consistency': result['consistency']}))
