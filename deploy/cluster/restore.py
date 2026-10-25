"""Restore a verified snapshot into a NEW directory for recovery rehearsal.

Never overlays a running service or existing data. Service promotion and logical
merging of divergent databases are separate, explicit operations.
"""
import argparse
import json
import os
import shutil
import uuid
from pathlib import Path
from snapshot import digest, safe_relative, verify_snapshot


def restore_snapshot(source, destination):
    source = Path(source).resolve()
    manifest = verify_snapshot(source)
    destination = Path(destination).absolute()
    if destination.exists() or destination.is_symlink():
        raise FileExistsError('restore destination must be new')
    if source == destination or source in destination.parents:
        raise ValueError('restore destination is inside snapshot')
    stage = destination.with_name(destination.name + '.restore-' + uuid.uuid4().hex)
    stage.mkdir(parents=True, mode=0o700)
    for record in manifest['files']:
        relative = Path(*safe_relative(record['path']).parts)
        target = stage / 'data' / relative
        target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        shutil.copyfile(source / 'data' / relative, target)
        os.chmod(target, 0o600)
        if target.stat().st_size != record['bytes'] or digest(target) != record['sha256']:
            raise ValueError('restored file verification failed')
    shutil.copyfile(source / 'manifest.json', stage / 'manifest.json')
    verify_snapshot(stage)
    os.rename(stage, destination)
    return {'node': manifest['node'], 'files': len(manifest['files']), 'restored': str(destination)}


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('source'); parser.add_argument('destination')
    args = parser.parse_args()
    print(json.dumps(restore_snapshot(args.source, args.destination)))
