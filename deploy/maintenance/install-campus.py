#!/usr/bin/env python3
import json,os,pathlib,subprocess
from provider_config import selected_provider_url

root=pathlib.Path('/home/worker/campus-stack/maintenance')
os.umask(0o077);root.mkdir(exist_ok=True)
worker=json.loads(pathlib.Path('/home/worker/campus-stack/worker-secrets/worker-config.json').read_text())
cfg=root/'config.json'
if not cfg.exists():
    value={'host':'campus','root':str(root),'relay_url':worker['relay_url'],'key_file':worker['relay_key_file'],
      'programs':[
      {'name':'Codex CLI','repo':'openai/codex','version':'0.160.1','asset_pattern':r'^codex-package-x86_64-unknown-linux-musl\.tar\.gz$','target':worker['codex_binary']},
      {'name':'Cockpit Tools','repo':'jlcodes99/cockpit-tools','version':'1.3.65','asset_pattern':r'^Cockpit\.Tools_[0-9.]+_amd64\.deb$','target':'/usr/bin/cockpit-tools'},
      {'name':'Clash Verge Rev','repo':'clash-verge-rev/clash-verge-rev','version':'2.5.7','asset_pattern':r'^Clash\.Verge_[0-9.]+_amd64\.deb$','target':'/usr/bin/clash-verge'}]}
    cfg.write_text(json.dumps(value,indent=2));os.chmod(cfg,0o600)
# Populate new probe settings on existing installations as well, preserving
# an operator's explicitly configured endpoint and credentials.
value=json.loads(cfg.read_text())
if 'gateway_url' not in value or 'gateway_key_file' not in value:
    codex=(pathlib.Path(worker['codex_home'])/'config.toml').read_text()
    value.setdefault('gateway_url',selected_provider_url(codex))
    value.setdefault('gateway_key_file',worker['api_key_file'])
    temp=cfg.with_suffix('.new');temp.write_text(json.dumps(value,indent=2));os.chmod(temp,0o600);os.replace(temp,cfg)
units=pathlib.Path('/home/worker/.config/systemd/user');units.mkdir(parents=True,exist_ok=True)
for name,action,calendar in [('morning','morning','*-*-* 07:00:00 Asia/Shanghai'),('usage','usage','*-*-* 00:00:00 Asia/Shanghai'),('retry','retry','*-*-* *:00/15:00 Asia/Shanghai')]:
    service=f'''[Unit]
Description=Deterministic campus maintenance ({name}), no AI
After=campus-ai-private-tunnel.service
Wants=campus-ai-private-tunnel.service
[Service]
Type=oneshot
ExecStart=/usr/bin/python3 {root}/campus-maintenance.py {action}
TimeoutStartSec=2h
UMask=0077
'''
    timer=f'''[Unit]
Description=Campus maintenance {name} (Beijing time)
[Timer]
OnCalendar={calendar}
Persistent=true
AccuracySec=1s
RandomizedDelaySec=0
[Install]
WantedBy=timers.target
'''
    (units/f'campus-maintenance-{name}.service').write_text(service)
    (units/f'campus-maintenance-{name}.timer').write_text(timer)
subprocess.run(['systemctl','--user','daemon-reload'],check=True)
subprocess.run(['systemctl','--user','enable','--now',*[f'campus-maintenance-{n}.timer' for n in ['morning','usage','retry']]],check=True)
print(json.dumps({'installed':True,'timezone':'Asia/Shanghai','timers':subprocess.check_output(['systemctl','--user','list-timers','--all','--no-pager'],text=True)}))
