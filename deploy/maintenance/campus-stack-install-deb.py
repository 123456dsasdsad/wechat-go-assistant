#!/usr/bin/python3
"""Root-owned narrow installer: only official verified Cockpit/Clash releases."""
import hashlib,json,os,pathlib,shutil,subprocess,sys,urllib.request,urllib.parse

ROOT=pathlib.Path('/home/worker/campus-stack/maintenance')
ROOT_BACKUP=pathlib.Path('/var/lib/campus-stack-maintenance')
ALLOWED={'Cockpit Tools':('jlcodes99/cockpit-tools','cockpit-tools'),'Clash Verge Rev':('clash-verge-rev/clash-verge-rev','clash-verge')}
def run():
    os.umask(0o077)
    if os.geteuid()!=0 or len(sys.argv)!=2:raise RuntimeError('invalid_invocation')
    original=pathlib.Path(sys.argv[1]).resolve(strict=True)
    if original.parent!=(ROOT/'staging').resolve() or original.suffix!='.deb':raise RuntimeError('unmanaged_path')
    staged=json.loads((ROOT/'updates-staged.json').read_text())
    records=[u for u in staged if u.get('archive')==str(original) and u.get('name') in ALLOWED]
    if len(records)!=1:raise RuntimeError('unmanaged_package')
    u=records[0];repo,package=ALLOWED[u['name']]
    cfg=json.loads((ROOT/'config.json').read_text());base=cfg['relay_url'];key=pathlib.Path(cfg['key_file']).read_text().strip()
    # Do not trust the user's staged digest or mutable path for privileged install.
    if base!='http://127.0.0.1:17441':raise RuntimeError('unapproved_bridge')
    url=base+'/maintenance/release?url='+urllib.parse.quote('https://api.github.com/repos/'+repo+'/releases/latest',safe='')
    req=urllib.request.Request(url,headers={'Authorization':'Bearer '+key})
    with urllib.request.urlopen(req,timeout=30) as resp:release=json.load(resp)
    assets=[a for a in release['assets'] if a['name']==original.name]
    if len(assets)!=1 or release.get('prerelease') or release.get('draft'):raise RuntimeError('not_stable_asset')
    digest=assets[0].get('digest','')
    if not digest.startswith('sha256:') or len(digest)!=71:raise RuntimeError('missing_official_digest')
    ROOT_BACKUP.mkdir(mode=0o700,exist_ok=True)
    trusted=ROOT_BACKUP/'verified-next.deb';h=hashlib.sha256()
    with original.open('rb') as src,trusted.open('wb') as dst:
        for b in iter(lambda:src.read(1024*1024),b''):h.update(b);dst.write(b)
    if 'sha256:'+h.hexdigest()!=digest:trusted.unlink();raise RuntimeError('checksum_mismatch')
    name=subprocess.check_output(['dpkg-deb','-f',str(trusted),'Package'],text=True).strip()
    if name!=package:raise RuntimeError('package_identity_mismatch')
    # Current shipped packages are retained for rollback before first auto update.
    backup=ROOT_BACKUP/(package+'-previous.deb')
    candidate=ROOT_BACKUP/(package+'-current.deb')
    if not candidate.exists():raise RuntimeError('rollback_package_missing')
    shutil.copy2(candidate,backup)
    try:
        subprocess.run(['dpkg','--install',str(trusted)],check=True,capture_output=True,timeout=540)
        audit=subprocess.check_output(['dpkg','--audit'],text=True,timeout=30)
        if audit.strip():raise RuntimeError('package_audit_failed')
        binary='/usr/bin/cockpit-tools' if package=='cockpit-tools' else '/usr/bin/clash-verge'
        # Dynamic loader dependency check avoids starting GUI or changing proxy state.
        output=subprocess.check_output(['ldd',binary],text=True,stderr=subprocess.STDOUT,timeout=15)
        if 'not found' in output:raise RuntimeError('missing_runtime_dependency')
        os.replace(trusted,candidate)
    except Exception:
        subprocess.run(['dpkg','--install',str(backup)],check=True,capture_output=True,timeout=120)
        raise
if __name__=='__main__':
    try:run()
    except Exception as e:print('verified_install_failed:'+type(e).__name__,file=sys.stderr);sys.exit(1)
