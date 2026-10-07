param([Parameter(Mandatory=$true)][string]$Root)
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
$taskRoot=[IO.Path]::GetFullPath($Root).TrimEnd('\')
$taskExpected='C:\CodexStack\cockpit-gateway\1.3.65\live'
if($taskRoot -ne $taskExpected){throw 'account_pool_root_rejected'}
$taskName='Cockpit-Account-Pool'
$taskBinary='C:\CodexStack\cockpit-gateway\1.3.65\cockpit-cliproxy.exe'
$taskKey=[IO.File]::ReadAllText((Join-Path $taskRoot 'client-key.txt')).Trim()
$null=& C:\CodexStack\maintenance\maintenance.exe -config C:\CodexStack\maintenance\config.json -action models
if($LASTEXITCODE -ne 0){throw 'account_model_catalog_pending'}
$taskCatalog=Get-Content -Raw -Encoding UTF8 C:\CodexStack\maintenance\model-catalog-latest.json|ConvertFrom-Json
if($taskCatalog.pending -gt 0){throw 'account_model_catalog_pending'}
$taskReady=$false
$null=Disable-ScheduledTask -TaskName $taskName
try {
 Stop-ScheduledTask -TaskName $taskName
 foreach($taskProcess in @(Get-Process | Where-Object {$_.Path -eq $taskBinary})) {
  Stop-Process -Id $taskProcess.Id -Force -ErrorAction SilentlyContinue
  Wait-Process -Id $taskProcess.Id -Timeout 5 -ErrorAction SilentlyContinue
  $taskRemaining=Get-Process -Id $taskProcess.Id -ErrorAction SilentlyContinue
  if($taskRemaining -and !$taskRemaining.HasExited){throw 'account_pool_process_did_not_stop'}
 }
} finally {$null=Enable-ScheduledTask -TaskName $taskName;Start-ScheduledTask -TaskName $taskName}
for($taskTry=0;$taskTry -lt 20;$taskTry++) {
 Start-Sleep -Milliseconds 750
 try {
  $taskResponse=Invoke-WebRequest 'http://127.0.0.1:17442/v1/models' -Headers @{Authorization=('Bearer '+$taskKey)} -UseBasicParsing -TimeoutSec 1
  if($taskResponse.StatusCode -eq 200){$taskReady=$true;break}
 }catch{}
}
if(!$taskReady){throw 'account_pool_reload_health_failed'}
if(@(Get-Process|Where-Object{$_.Path -eq $taskBinary}).Count -ne 1){throw 'account_pool_instance_count_invalid'}
