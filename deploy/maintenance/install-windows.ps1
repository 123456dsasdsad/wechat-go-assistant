$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
$taskRoot='C:\CodexStack\maintenance';$taskUtf8=[Text.UTF8Encoding]::new($false)
New-Item -ItemType Directory -Path $taskRoot -Force|Out-Null
& icacls.exe $taskRoot /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F'|Out-Null
if($LASTEXITCODE -ne 0){throw 'Private maintenance ACL failed'}
if((Get-TimeZone).Id -ne 'China Standard Time'){throw 'Unexpected timezone; explicit Beijing schedule required'}
$taskRelay=Get-Content -Raw -Encoding UTF8 C:\CodexStack\wechat-go\data\relay\config.json|ConvertFrom-Json
$taskCfgPath=Join-Path $taskRoot 'config.json'
if(!(Test-Path $taskCfgPath)){
 $taskCaddyVersion=(& C:\CodexStack\caddy\caddy.exe version) -split ' ' | Select-Object -First 1
 $taskConfig=@{
  host='cloud';root=$taskRoot;relay_url='http://127.0.0.1:17440';key_file=$taskRelay.key_file
  gateway_root='C:\CodexStack\cockpit-gateway\1.3.65\live';usage_log='C:\CodexStack\cockpit-gateway\1.3.65\live\events.log'
  programs=@(
   @{name='Codex CLI';repo='openai/codex';version='0.160.1';asset_pattern='^codex-x86_64-pc-windows-msvc\.exe\.zip$';target='C:\CodexStack\codex-cli\0.160.1\codex-x86_64-pc-windows-msvc.exe'},
   @{name='Cockpit gateway';repo='jlcodes99/cockpit-tools';version='1.3.65';asset_pattern='^Cockpit\.Tools_[0-9.]+_x64-portable\.zip$';target='C:\CodexStack\cockpit-gateway\1.3.65\cockpit-cliproxy.exe'},
   @{name='Caddy';repo='caddyserver/caddy';version=$taskCaddyVersion;asset_pattern='^caddy_[0-9.]+_windows_amd64\.zip$';target='C:\CodexStack\caddy\caddy.exe'}
  )
 }
 [IO.File]::WriteAllText($taskCfgPath,($taskConfig|ConvertTo-Json -Depth 8),$taskUtf8)
}
$taskSettings=New-ScheduledTaskSettingsSet -StartWhenAvailable -MultipleInstances IgnoreNew -ExecutionTimeLimit (New-TimeSpan -Hours 2) -RestartCount 2 -RestartInterval (New-TimeSpan -Minutes 5)
foreach($taskSpec in @(@{name='CampusStack-Morning-0700';action='morning';trigger=(New-ScheduledTaskTrigger -Daily -At '07:00')},@{name='CampusStack-Usage-0000';action='usage';trigger=(New-ScheduledTaskTrigger -Daily -At '00:00')},@{name='CampusStack-Retry-15min';action='retry';trigger=(New-ScheduledTaskTrigger -Once -At (Get-Date).Date.AddMinutes(5) -RepetitionInterval (New-TimeSpan -Minutes 15))})){
 $taskAction=New-ScheduledTaskAction -Execute 'powershell.exe' -Argument ('-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "'+$taskRoot+'\windows-maintenance.ps1" -Action '+$taskSpec.action)
 Register-ScheduledTask -TaskName $taskSpec.name -Action $taskAction -Trigger $taskSpec.trigger -Settings $taskSettings -User 'SYSTEM' -RunLevel Highest -Description 'Deterministic server maintenance; Beijing time; no AI calls' -Force|Out-Null
}
@{installed=$true;timezone=[string](Get-TimeZone).Id;tasks=@(Get-ScheduledTask -TaskName 'CampusStack-*'|ForEach-Object{$taskInfo=Get-ScheduledTaskInfo $_;@{name=$_.TaskName;state=[string]$_.State;nextRun=$taskInfo.NextRunTime.ToString('o')}})}|ConvertTo-Json -Depth 4 -Compress
