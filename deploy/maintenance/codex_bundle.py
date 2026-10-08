"""Stage official Codex packages including their matching helper executables."""
import json,os,pathlib,re,shutil,subprocess,tarfile,tempfile,time

MANAGED={'codex','codex-app-server','codex-code-mode-host','codex-command-runner','bwrap','bin','codex-package.json','codex-resources','codex-path'}

def stage_bundle(archive,target,root,latest):
    stage=pathlib.Path(tempfile.mkdtemp(prefix='codex-bundle-',dir=root))
    bundle=stage/'live';bundle.mkdir()
    # Keep operator notes but never carry old packaged executables/resources into
    # a different release. Preserve the official layout for resource discovery.
    for item in target.parent.iterdir():
        if item.name in MANAGED:continue
        if item.is_dir() and not item.is_symlink():shutil.copytree(item,bundle/item.name,symlinks=True)
        else:shutil.copy2(item,bundle/item.name,follow_symlinks=False)
    seen=set()
    with tarfile.open(archive,'r:gz') as tf:
        for member in tf.getmembers():
            path=pathlib.PurePosixPath(member.name)
            if path.is_absolute() or '..' in path.parts or '\\' in member.name or not path.parts or path.parts[0] not in {'bin','codex-package.json','codex-resources','codex-path'}:raise RuntimeError('unsafe_bundle_path')
            name=path.as_posix()
            if member.isdir():(bundle/path).mkdir(parents=True,exist_ok=True);continue
            if not member.isfile() or name in seen:raise RuntimeError('invalid_bundle_member')
            seen.add(name);destination=bundle/path;destination.parent.mkdir(parents=True,exist_ok=True)
            with tf.extractfile(member) as src,destination.open('wb') as dst:shutil.copyfileobj(src,dst,1024*1024)
            destination.chmod(0o700 if member.mode&0o111 else 0o600)
    if not {'bin/codex','bin/codex-code-mode-host','codex-package.json'}.issubset(seen):raise RuntimeError('incomplete_codex_bundle')
    expected=re.search(r'\d+\.\d+\.\d+',latest)
    manifest=json.loads((bundle/'codex-package.json').read_text())
    if not expected or manifest.get('version')!=expected.group() or manifest.get('layoutVersion')!=1 or manifest.get('entrypoint')!='bin/codex' or manifest.get('variant')!='codex':raise RuntimeError('codex_package_manifest_mismatch')
    for name in ['codex','codex-code-mode-host']:(bundle/name).symlink_to('bin/'+name)
    version=subprocess.check_output([str(bundle/'codex'),'--version'],text=True,timeout=15).strip()
    if not expected or version.split()[-1]!=expected.group():raise RuntimeError('codex_version_mismatch')
    schema=stage/'schema';schema.mkdir()
    subprocess.run([str(bundle/'codex'),'app-server','generate-json-schema','--out',str(schema)],check=True,capture_output=True,timeout=30)
    protocol=''.join(p.read_text(errors='replace') for p in schema.rglob('*.json'))
    if not all(s in protocol for s in ['turn/steer','thread/resume','turn/start']):raise RuntimeError('required_protocol_missing')
    if bundle.stat().st_dev!=target.parent.stat().st_dev:raise RuntimeError('bundle_volume_mismatch')
    return bundle

def switch_bundle(bundle,target):
    live=target.parent;backup=live.with_name(live.name+'.maintenance-previous')
    if backup.exists():backup=live.with_name(live.name+'.maintenance-previous-'+str(time.time_ns()))
    os.replace(live,backup)
    try:os.replace(bundle,live)
    except Exception:os.replace(backup,live);raise
    return backup

def restore_bundle(target,backup):
    live=target.parent;failed=live.with_name(live.name+'.maintenance-failed-'+str(time.time_ns()))
    if live.exists():os.replace(live,failed)
    os.replace(backup,live)
