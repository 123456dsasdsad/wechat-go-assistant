param([ValidateSet('morning','usage','accounts','retry','updates')][string]$Action='morning')
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
$taskRoot='C:\CodexStack\maintenance';$taskExe=Join-Path $taskRoot 'maintenance.exe';$taskConfig=Join-Path $taskRoot 'config.json'
function Invoke-Maintenance([string]$taskAction,[string[]]$taskExtra=@()) {
 # Windows PowerShell 5.1 otherwise decodes native UTF-8 JSON using its OEM page.
 $taskArgs=@('-config',$taskConfig,'-action',$taskAction)+$taskExtra
 if(@($taskArgs|Where-Object{$_ -match '["\r\n]'}).Count){throw 'maintenance_arguments_invalid'}
 $taskStart=[Diagnostics.ProcessStartInfo]::new()
 $taskStart.FileName=$taskExe;$taskStart.Arguments=(($taskArgs|ForEach-Object{'"'+$_+'"'}) -join ' ')
 $taskStart.UseShellExecute=$false;$taskStart.CreateNoWindow=$true
 $taskStart.RedirectStandardOutput=$true;$taskStart.RedirectStandardError=$true
 $taskStart.StandardOutputEncoding=[Text.UTF8Encoding]::new($false);$taskStart.StandardErrorEncoding=[Text.UTF8Encoding]::new($false)
 $taskProcess=[Diagnostics.Process]::Start($taskStart)
 try {
  $taskOut=$taskProcess.StandardOutput.ReadToEndAsync();$taskErr=$taskProcess.StandardError.ReadToEndAsync()
  $taskProcess.WaitForExit();$taskRaw=$taskOut.Result;$null=$taskErr.Result
  if($taskProcess.ExitCode -ne 0){throw ('maintenance_'+$taskAction+'_failed')}
  return ($taskRaw|ConvertFrom-Json)
 }finally{$taskProcess.Dispose()}
}
function Test-Idle {
 # After migration the JSON files are backups, not current task state.
 try {
  $taskCfg=Get-Content -Raw -Encoding UTF8 $taskConfig|ConvertFrom-Json
  $taskKey=[IO.File]::ReadAllText($taskCfg.key_file).Trim()
  $taskIdle=Invoke-RestMethod ($taskCfg.relay_url+'/maintenance/idle') -Headers @{Authorization=('Bearer '+$taskKey)} -TimeoutSec 15
  return ($taskIdle.idle -eq $true)
 }catch{return $false}
}
function Apply-RequestedUpdates {
 $taskCfg=Get-Content -Raw -Encoding UTF8 $taskConfig|ConvertFrom-Json;$taskKey=[IO.File]::ReadAllText($taskCfg.key_file).Trim()
 $taskResponse=Invoke-RestMethod ($taskCfg.relay_url+'/maintenance/update-requests?host=cloud') -Headers @{Authorization=('Bearer '+$taskKey)} -TimeoutSec 15
 $taskRequests=@($taskResponse.requests|Where-Object{$_})
 if(!$taskRequests.Count){return $false}
 Apply-Updates
 foreach($taskRequest in $taskRequests){
  $null=Invoke-RestMethod ($taskCfg.relay_url+'/maintenance/update-requests/complete') -Method POST -Headers @{Authorization=('Bearer '+$taskKey)} -ContentType 'application/json' -Body (@{host='cloud';id=$taskRequest.id}|ConvertTo-Json -Compress) -TimeoutSec 15
 }
 return $true
}
function Lease([string]$taskAction,[string]$taskID='') {
 $taskCfg=Get-Content -Raw -Encoding UTF8 $taskConfig|ConvertFrom-Json;$taskKey=[IO.File]::ReadAllText($taskCfg.key_file).Trim()
 try{return Invoke-RestMethod ($taskCfg.relay_url+'/maintenance/lease') -Method POST -Headers @{Authorization=('Bearer '+$taskKey)} -ContentType 'application/json' -Body (@{Action=$taskAction;Lease=$taskID}|ConvertTo-Json) -TimeoutSec 15}
 catch{if($_.Exception.Response -and [int]$_.Exception.Response.StatusCode -eq 409){return $null};throw 'maintenance_lease_unavailable'}
}
function Restart-OwnedTask([string]$taskName,[string]$taskBinary) {
 $null=Disable-ScheduledTask -TaskName $taskName
 try{Stop-OwnedTask $taskName $taskBinary}
 finally{$null=Enable-ScheduledTask -TaskName $taskName;Start-ScheduledTask -TaskName $taskName}
}
function Stop-OwnedTask([string]$taskName,[string]$taskBinary) {
 $taskDefinition=Get-ScheduledTask -TaskName $taskName
 $taskWrappers=@()
 foreach($taskCandidate in @(Get-CimInstance Win32_Process|Where-Object{$_.Name -in @('cmd.exe','powershell.exe','pwsh.exe')})){
  foreach($taskAction in $taskDefinition.Actions){
   if($taskAction.Arguments.Length -gt 8 -and [IO.Path]::GetFileName($taskAction.Execute) -eq $taskCandidate.Name -and $taskCandidate.CommandLine -and $taskCandidate.CommandLine.IndexOf($taskAction.Arguments,[StringComparison]::OrdinalIgnoreCase) -ge 0){$taskWrappers+=$taskCandidate.ProcessId;break}
  }
 }
 Stop-ScheduledTask -TaskName $taskName
 foreach($taskWrapperPID in $taskWrappers){Stop-Process -Id $taskWrapperPID -Force -ErrorAction SilentlyContinue;Wait-Process -Id $taskWrapperPID -Timeout 5 -ErrorAction SilentlyContinue}
 foreach($taskProcess in @(Get-Process|Where-Object{$_.Path -eq $taskBinary})){
  Stop-Process -Id $taskProcess.Id -Force -ErrorAction SilentlyContinue
  Wait-Process -Id $taskProcess.Id -Timeout 10 -ErrorAction SilentlyContinue
  $taskRemaining=Get-Process -Id $taskProcess.Id -ErrorAction SilentlyContinue
  if($taskRemaining -and !$taskRemaining.HasExited){throw 'owned_process_did_not_stop'}
 }
}
function Copy-ManagedBinary([string]$taskSource,[string]$taskDestination) {
 # Windows may retain a loaded executable briefly after its process exits.
 for($taskCopyAttempt=0;$taskCopyAttempt -lt 10;$taskCopyAttempt++){
  try{Copy-Item -LiteralPath $taskSource -Destination $taskDestination -Force;return}
  catch [IO.IOException]{if($taskCopyAttempt -eq 9){throw};Start-Sleep -Milliseconds 500}
 }
}
function Test-Gateway {
 $taskKey=[IO.File]::ReadAllText('C:\CodexStack\cockpit-gateway\1.3.65\live\client-key.txt').Trim()
 try{return (Invoke-WebRequest 'http://127.0.0.1:17442/v1/models' -Headers @{Authorization=('Bearer '+$taskKey)} -UseBasicParsing -TimeoutSec 10).StatusCode -eq 200}catch{return $false}
}
function Wait-ManagedHealth([string]$taskProgram,[string]$taskBinary) {
 for($taskProbe=0;$taskProbe -lt 12;$taskProbe++){
  if($taskProgram -eq 'Cockpit gateway'){if(Test-Gateway){return $true}}
  elseif(Get-Process|Where-Object{$_.Path -eq $taskBinary}){
   $taskRelayCfg=Get-Content -Raw -Encoding UTF8 C:\CodexStack\wechat-go\data\relay\config.json|ConvertFrom-Json
   try{if((Invoke-WebRequest ($taskRelayCfg.public_url.TrimEnd('/')+'/') -UseBasicParsing -TimeoutSec 5).StatusCode -eq 200){return $true}}catch{}
  }
  Start-Sleep -Seconds 2
 };return $false
}
function Apply-Updates {
 $taskUpdates=@(Invoke-Maintenance 'updates')
 $taskPending=@($taskUpdates|Where-Object{$_.archive})
 $taskLease=$null;if($taskPending.Count){$taskLease=Lease 'acquire'}
 try {
  foreach($taskUpdate in $taskPending){
   if(!$taskLease -or !(Test-Idle)){$taskUpdate.state='AI 任务运行中，延后自动安装';continue}
   if((Get-FileHash -LiteralPath $taskUpdate.archive).Hash.ToLower() -ne $taskUpdate.sha256){$taskUpdate.state='暂存校验失败，保留现版';continue}
   if((Get-PSDrive C).Free -lt 512MB){$taskUpdate.state='磁盘余量不足，保留现版';continue}
   $taskExtract=Join-Path $taskRoot ('staging\extract-'+$taskUpdate.name)
   if(Test-Path -LiteralPath $taskExtract){$taskResolved=[IO.Path]::GetFullPath($taskExtract);if(!$taskResolved.StartsWith($taskRoot+'\staging\')){throw 'Unsafe extraction path'};Remove-Item -LiteralPath $taskResolved -Recurse -Force}
   Expand-Archive -LiteralPath $taskUpdate.archive -DestinationPath $taskExtract
   $taskFileName=switch($taskUpdate.name){'Codex CLI'{'codex-x86_64-pc-windows-msvc.exe'}'Cockpit gateway'{'cockpit-cliproxy.exe'}'Caddy'{'caddy.exe'}default{throw 'Unapproved software'}}
   $taskCandidates=@(Get-ChildItem -LiteralPath $taskExtract -Recurse -File|Where-Object{$_.Name -eq $taskFileName})
   if($taskCandidates.Count -ne 1){$taskUpdate.state='安装内容不匹配，保留现版';continue}
   $taskNew=$taskCandidates[0].FullName;$taskTarget=$taskUpdate.target;$taskBackup=$taskTarget+'.maintenance-previous'
   if($taskUpdate.name -eq 'Caddy' -or $taskUpdate.name -eq 'Codex CLI'){
    # Caddy writes successful validation notices to stderr on Windows PowerShell.
    $taskSavedPreference=$ErrorActionPreference
    try{$ErrorActionPreference='Continue';if($taskUpdate.name -eq 'Caddy'){& $taskNew validate --config C:\CodexStack\caddy\Caddyfile *> $null}else{& $taskNew --version *> $null};$taskValidationExit=$LASTEXITCODE}
    finally{$ErrorActionPreference=$taskSavedPreference}
    if($taskValidationExit -ne 0){$taskUpdate.state='新版启动/配置校验失败，保留现版';continue}
   }
   Copy-Item -LiteralPath $taskTarget -Destination $taskBackup -Force
   Copy-Item -LiteralPath $taskConfig -Destination ($taskConfig+'.maintenance-previous') -Force
   $taskService=switch($taskUpdate.name){'Cockpit gateway'{'Cockpit-Account-Pool'}'Caddy'{'Codex-Caddy'}default{''}}
   $taskInstallStage='stop'
   try {
    if($taskService){$null=Disable-ScheduledTask -TaskName $taskService;Stop-OwnedTask $taskService $taskTarget}
    $taskInstallStage='replace'
    Copy-ManagedBinary $taskNew $taskTarget
    $taskInstallStage='start'
    if($taskService){$null=Enable-ScheduledTask -TaskName $taskService;Start-ScheduledTask -TaskName $taskService}
    $taskInstallStage='health'
    if($taskService -and !(Wait-ManagedHealth $taskUpdate.name $taskTarget)){throw 'managed_health_failed'}
    $taskInstallStage='save'
    $taskCfg=Get-Content -Raw -Encoding UTF8 $taskConfig|ConvertFrom-Json
    foreach($taskProgram in $taskCfg.programs){if($taskProgram.name -eq $taskUpdate.name){$taskProgram.version=$taskUpdate.latest}}
    [IO.File]::WriteAllText($taskConfig,($taskCfg|ConvertTo-Json -Depth 10),[Text.UTF8Encoding]::new($false))
    $taskUpdate.state='已自动更新，健康检查通过'
    $taskArchiveResolved=[IO.Path]::GetFullPath($taskUpdate.archive)
    if($taskArchiveResolved.StartsWith($taskRoot+'\staging\')){Remove-Item -LiteralPath $taskArchiveResolved -Force -ErrorAction SilentlyContinue}
   }catch{
    $taskFailureCategory=$_.Exception.GetType().Name
    if($taskService){Stop-OwnedTask $taskService $taskTarget}
    Copy-ManagedBinary $taskBackup $taskTarget
    Copy-Item -LiteralPath ($taskConfig+'.maintenance-previous') -Destination $taskConfig -Force
    $taskUpdate.state='更新失败，已回滚（'+$taskInstallStage+'/'+$taskFailureCategory+'）'
   }finally{if($taskService){$null=Enable-ScheduledTask -TaskName $taskService;Start-ScheduledTask -TaskName $taskService}}
   # Keep one binary rollback; discard only verified managed temporary extraction.
   if(Test-Path -LiteralPath $taskExtract){$taskResolved=[IO.Path]::GetFullPath($taskExtract);if($taskResolved.StartsWith($taskRoot+'\staging\')){Remove-Item -LiteralPath $taskResolved -Recurse -Force}}
  }
 } finally {if($taskLease){$null=Lease 'release' $taskLease.lease}}
 $taskResult=Join-Path $taskRoot 'updates-result.json';[IO.File]::WriteAllText($taskResult,(ConvertTo-Json -InputObject @($taskUpdates) -Depth 8),[Text.UTF8Encoding]::new($false))
 $null=Invoke-Maintenance 'publish' @('-input',$taskResult)
}
function Check-Accounts {
 $taskLease=Lease 'acquire'
 if(!$taskLease){[IO.File]::WriteAllText((Join-Path $taskRoot 'accounts-pending.flag'),'1');return}
 try {
  if(!(Test-Idle)){[IO.File]::WriteAllText((Join-Path $taskRoot 'accounts-pending.flag'),'1');return}
  $null=Invoke-Maintenance 'accounts'
  $taskDay=[TimeZoneInfo]::ConvertTimeBySystemTimeZoneId([DateTime]::UtcNow,'China Standard Time').ToString('yyyy-MM-dd')
  $taskSummary=Get-Content -Raw -Encoding UTF8 (Join-Path $taskRoot ('accounts-'+$taskDay+'.json'))|ConvertFrom-Json
  $taskCatalog=Invoke-Maintenance 'models'
  if($taskSummary.reload_required -or $taskCatalog.reload_required){Restart-OwnedTask 'Cockpit-Account-Pool' 'C:\CodexStack\cockpit-gateway\1.3.65\cockpit-cliproxy.exe';if(!(Wait-ManagedHealth 'Cockpit gateway' 'C:\CodexStack\cockpit-gateway\1.3.65\cockpit-cliproxy.exe')){throw 'Refreshed pool health check failed'}}
  if($taskCatalog.pending -gt 0){[IO.File]::WriteAllText((Join-Path $taskRoot 'accounts-pending.flag'),'1')}
  else{Remove-Item -LiteralPath (Join-Path $taskRoot 'accounts-pending.flag') -ErrorAction SilentlyContinue}
 }finally{$null=Lease 'release' $taskLease.lease}
}
$taskMutexName='Global\CampusStackMaintenance';if($Action -eq 'usage'){$taskMutexName+='Usage'}
$taskMutex=[Threading.Mutex]::new($false,$taskMutexName)
try{$taskLockAcquired=if($Action -eq 'morning'){$taskMutex.WaitOne()}else{$taskMutex.WaitOne(0)}}catch [Threading.AbandonedMutexException]{$taskLockAcquired=$true}
if(!$taskLockAcquired){$taskMutex.Dispose();exit 0}
try{
 switch($Action){
  'morning'{Apply-Updates;Check-Accounts}
  'updates'{Apply-Updates}
  'accounts'{Check-Accounts}
  'usage'{$null=Invoke-Maintenance 'usage'}
  'retry'{$null=Invoke-Maintenance 'retry-publish';$taskRequested=Apply-RequestedUpdates;if(Test-Path (Join-Path $taskRoot 'accounts-pending.flag')){Check-Accounts};$taskStaged=Join-Path $taskRoot 'updates-result.json';if(!$taskRequested -and (Test-Path $taskStaged)){$taskLast=Get-Content -Raw -Encoding UTF8 $taskStaged|ConvertFrom-Json;if(@($taskLast|Where-Object{$_.state -eq 'AI 任务运行中，延后自动安装'}).Count -and (Test-Idle)){Apply-Updates}}}
 }
 [IO.File]::WriteAllText((Join-Path $taskRoot ('last-'+$Action+'.json')),(@{ok=$true;utc=[DateTime]::UtcNow.ToString('o')}|ConvertTo-Json),[Text.UTF8Encoding]::new($false))
}catch{
 [IO.File]::WriteAllText((Join-Path $taskRoot ('last-'+$Action+'.json')),(@{ok=$false;utc=[DateTime]::UtcNow.ToString('o');category=$_.Exception.GetType().Name;line=$_.InvocationInfo.ScriptLineNumber}|ConvertTo-Json),[Text.UTF8Encoding]::new($false))
 throw ('maintenance_runner_'+$Action+'_failed')
}finally{$taskMutex.ReleaseMutex();$taskMutex.Dispose()}
