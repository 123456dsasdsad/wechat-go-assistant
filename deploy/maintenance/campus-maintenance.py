#!/usr/bin/env python3
"""Native maintenance runner. Network artifacts come over the existing tunnel."""
import contextlib, datetime, fcntl, hashlib, json, os, pathlib, shutil, subprocess, sys, tarfile, urllib.request, urllib.error

ROOT=pathlib.Path('/home/worker/campus-stack/maintenance')
CONFIG=ROOT/'config.json'
EXE=ROOT/'maintenance'

def atomic(path,value):
    temp=path.with_suffix(path.suffix+'.tmp')
    temp.write_text(json.dumps(value,ensure_ascii=False,indent=2),encoding='utf-8')
    os.chmod(temp,0o600)
    os.replace(temp,path)

def invoke(action,*extra):
    p=subprocess.run([str(EXE),'-config',str(CONFIG),'-action',action,*extra],capture_output=True,timeout=1500)
    if p.returncode: raise RuntimeError('maintenance_'+action+'_failed')
    return json.loads(p.stdout)

def unit(*args):
    subprocess.run(['systemctl','--user',*args],check=True,capture_output=True,timeout=30)

def idle(cfg):
    key=pathlib.Path(cfg['key_file']).read_text().strip()
    req=urllib.request.Request(cfg['relay_url']+'/maintenance/idle',headers={'Authorization':'Bearer '+key})
    with urllib.request.urlopen(req,timeout=10) as resp:return json.load(resp)['idle']

@contextlib.contextmanager
def maintenance_lease(cfg):
    key=pathlib.Path(cfg['key_file']).read_text().strip()
    def call(body):
        req=urllib.request.Request(cfg['relay_url']+'/maintenance/lease',data=json.dumps(body).encode(),headers={'Authorization':'Bearer '+key,'Content-Type':'application/json'})
        with urllib.request.urlopen(req,timeout=15) as resp:return json.load(resp)
    lease=None
    try:
        try:lease=call({'Action':'acquire'})['lease']
        except urllib.error.HTTPError as e:
            if e.code!=409:raise
        yield bool(lease)
    finally:
        if lease:call({'Action':'release','Lease':lease})

def sha(path):
    h=hashlib.sha256()
    with path.open('rb') as f:
        for b in iter(lambda:f.read(1024*1024),b''):h.update(b)
    return h.hexdigest()

def install_deb(archive):
    # Root helper accepts only verified release assets from an allowlisted repository.
    subprocess.run(['sudo','-n','/usr/local/sbin/campus-stack-install-deb',str(archive)],check=True,capture_output=True,timeout=600)

def updates():
    cfg=json.loads(CONFIG.read_text())
    staged=invoke('updates')
    for u in staged:
        if not u.get('archive'):continue
        archive=pathlib.Path(u['archive'])
        if sha(archive)!=u['sha256']:
            u['state']='暂存校验失败，保留现版';continue
        if not idle(cfg):
            u['state']='AI 任务运行中，延后自动安装';continue
        try:
          with maintenance_lease(cfg) as locked:
            if not locked:
                u['state']='AI 任务运行中，延后自动安装';continue
            if u['name']=='Codex CLI':
                target=pathlib.Path(u['target']);stage=target.with_name(target.name+'.maintenance-new');backup=target.with_name(target.name+'.maintenance-previous')
                with tarfile.open(archive,'r:gz') as tf:
                    members=[m for m in tf.getmembers() if pathlib.PurePosixPath(m.name).name in ('codex','codex-x86_64-unknown-linux-gnu') and m.isfile()]
                    if len(members)!=1:raise RuntimeError('archive_content_mismatch')
                    with tf.extractfile(members[0]) as src,stage.open('wb') as dst:shutil.copyfileobj(src,dst,1024*1024)
                os.chmod(stage,0o700)
                subprocess.run([str(stage),'--version'],check=True,capture_output=True,timeout=15)
                # Check required app-server API schemas without invoking a model.
                schema=ROOT/'schema-check';schema.mkdir(exist_ok=True)
                subprocess.run([str(stage),'app-server','generate-json-schema','--out',str(schema)],check=True,capture_output=True,timeout=30)
                protocol=''.join(f.read_text(errors='replace') for f in schema.rglob('*.json'))
                if not all(s in protocol for s in ['turn/steer','thread/resume','turn/start']):raise RuntimeError('required_protocol_missing')
                if not idle(cfg):u['state']='AI 任务运行中，延后自动安装';continue
                unit('stop','campus-wechat-worker.service')
                try:
                    # Service is stopped before switching; an in-flight task is never killed.
                    if not idle(cfg):raise RuntimeError('job_started_during_update')
                    shutil.copy2(target,backup);os.replace(stage,target)
                    unit('start','campus-wechat-worker.service')
                    unit('is-active','--quiet','campus-wechat-worker.service')
                except Exception:
                    if backup.exists():shutil.copy2(backup,target)
                    unit('start','campus-wechat-worker.service');raise
            elif u['name'] in ('Cockpit Tools','Clash Verge Rev'):
                install_deb(archive)
                package='cockpit-tools' if u['name']=='Cockpit Tools' else 'clash-verge'
                version=subprocess.check_output(['dpkg-query','-W','-f=${Version}',package],text=True,timeout=15).strip()
                if version!=u['latest'].removeprefix('v'):raise RuntimeError('package_version_mismatch')
            else:raise RuntimeError('unapproved_software')
            for p in cfg['programs']:
                if p['name']==u['name']:p['version']=u['latest']
            atomic(CONFIG,cfg);u['state']='已自动更新，健康检查通过'
            archive.unlink(missing_ok=True)
        except Exception as ex:
            u['state']='安装失败，保留/恢复现版（'+type(ex).__name__+'）'
    result=ROOT/'updates-result.json';atomic(result,staged);invoke('publish','-input',str(result))
    return staged

def run():
    os.umask(0o077);ROOT.mkdir(exist_ok=True)
    action=sys.argv[1] if len(sys.argv)>1 else 'morning'
    # Midnight retry and usage timers can coincide; accounting must never be skipped.
    with (ROOT/('runner-usage.lock' if action=='usage' else 'runner.lock')).open('w') as lock:
        # Persistent morning runs wait for any catch-up retry after reboot.
        try:fcntl.flock(lock,fcntl.LOCK_EX if action=='morning' else fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError:return
        if action=='morning':updates();invoke('accounts')
        elif action=='usage':invoke('usage')
        elif action=='retry':
            invoke('retry-publish')
            last=ROOT/'updates-result.json'
            if last.exists() and any(u['state']=='AI 任务运行中，延后自动安装' for u in json.loads(last.read_text())) and idle(json.loads(CONFIG.read_text())):updates()
        else:raise RuntimeError('unknown_action')
        atomic(ROOT/('last-'+action+'.json'),{'ok':True,'utc':datetime.datetime.now(datetime.timezone.utc).isoformat()})

if __name__=='__main__':
    try:run()
    except Exception as e:print('maintenance_failed:'+type(e).__name__,file=sys.stderr);sys.exit(1)
