param(
 [string]$RelayConfig='C:\CodexStack\wechat-go\data\relay\config.json',
 [Parameter(Mandatory=$true)][string]$MaintenanceCandidate,
 [Parameter(Mandatory=$true)][string]$CandidateSHA256,
 [string[]]$RequiredModels=@('gpt-5.6-luna')
)
$ErrorActionPreference='Stop';$ProgressPreference='SilentlyContinue'
$taskCfg=Get-Content -Raw -Encoding UTF8 -LiteralPath $RelayConfig|ConvertFrom-Json
$taskRoot=[IO.Path]::GetFullPath($taskCfg.cockpit_root).TrimEnd('\')
if($taskRoot -ne 'C:\CodexStack\cockpit-gateway\1.3.65\live'){throw 'gateway_root_rejected'}
$taskMaintenance='C:\CodexStack\maintenance\maintenance.exe'
$taskCandidate=[IO.Path]::GetFullPath($MaintenanceCandidate)
if(!$taskCandidate.StartsWith('C:\CodexStack\updates\',[StringComparison]::OrdinalIgnoreCase) -or $CandidateSHA256 -notmatch '^[a-fA-F0-9]{64}$'){throw 'maintenance_candidate_rejected'}
if((Get-FileHash -LiteralPath $taskCandidate -Algorithm SHA256).Hash -ne $CandidateSHA256){throw 'maintenance_candidate_hash_mismatch'}
foreach($taskModel in $RequiredModels){if($taskModel -notmatch '^[a-zA-Z0-9][a-zA-Z0-9._-]{0,100}$'){throw 'required_model_rejected'}}
$taskMutex=[Threading.Mutex]::new($false,'Global\CampusStackMaintenance')
$taskLocked=$false;$taskLease=$null;$taskChanged=$false;$taskReady=$false;$taskStage='lock'
$taskBackup='C:\CodexStack\updates\gateway-repair-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ')
$taskOriginals=@{}
function Save-TaskJSON([string]$Path,$Value){
 $taskTemporary=$Path+'.repair-'+[Guid]::NewGuid().ToString('N')
 try{[IO.File]::WriteAllText($taskTemporary,($Value|ConvertTo-Json -Depth 40),[Text.UTF8Encoding]::new($false));[IO.File]::Replace($taskTemporary,$Path,(Join-Path $taskBackup ((Split-Path $Path -Leaf)+'.atomic-before')))}
 finally{if(Test-Path -LiteralPath $taskTemporary){Remove-Item -LiteralPath $taskTemporary -Force}}
}
try{
 try{$taskLocked=$taskMutex.WaitOne(0)}catch [Threading.AbandonedMutexException]{$taskLocked=$true}
 if(!$taskLocked){throw 'maintenance_busy'}
 $taskRelayKey=[IO.File]::ReadAllText($taskCfg.key_file).Trim()
 $taskLease=Invoke-RestMethod ('http://'+$taskCfg.listen+'/maintenance/lease') -Method POST -Headers @{Authorization=('Bearer '+$taskRelayKey)} -ContentType 'application/json' -Body '{"Action":"acquire"}' -TimeoutSec 10
 if(!$taskLease.lease){throw 'maintenance_lease_missing'}
 if(@(Get-Process|Where-Object{$_.Path -eq $taskMaintenance}).Count -gt 0){throw 'maintenance_binary_busy'}
 # The relay lease covers campus jobs; gateway events also cover external clients.
 $taskStarts=@{};$taskEnds=@{}
 foreach($taskLine in @(Get-Content -LiteralPath ($taskRoot+'\events.log') -Tail 1200)){
  try{$taskEvent=$taskLine|ConvertFrom-Json}catch{continue}
  if($taskEvent.type -eq 'request_started'){$taskStarts[$taskEvent.requestId]=$taskEvent.startedAtMs}
  if($taskEvent.type -in @('request_completed','stream_completed','usage')){$taskEnds[$taskEvent.requestId]=$true}
 }
 $taskRecent=[DateTimeOffset]::UtcNow.AddMinutes(-20).ToUnixTimeMilliseconds()
 foreach($taskID in $taskStarts.Keys){if(!$taskEnds.ContainsKey($taskID) -and $taskStarts[$taskID] -gt $taskRecent){throw 'external_inference_busy'}}
 $taskStage='backup'
 $null=New-Item -ItemType Directory -Path $taskBackup -Force
 $null=& icacls.exe $taskBackup /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F'
 if($LASTEXITCODE -ne 0){throw 'gateway_backup_acl_failed'}
 $taskPaths=@($taskRoot+'\config.json';$taskRoot+'\manifest.json')+@(Get-ChildItem -LiteralPath ($taskRoot+'\auths') -Filter '*.json'|Select-Object -ExpandProperty FullName)
 foreach($taskPath in $taskPaths){$taskOriginals[$taskPath]=[IO.File]::ReadAllBytes($taskPath);$taskName=if((Split-Path $taskPath -Parent) -eq ($taskRoot+'\auths')){'auth-'+(Split-Path $taskPath -Leaf)}else{Split-Path $taskPath -Leaf};Copy-Item -LiteralPath $taskPath -Destination ($taskBackup+'\'+$taskName)}
 Copy-Item -LiteralPath $taskMaintenance -Destination ($taskBackup+'\maintenance.exe')
 $taskManifest=Get-Content -Raw -Encoding UTF8 -LiteralPath ($taskRoot+'\manifest.json')|ConvertFrom-Json
 $taskModelIDs=@($taskManifest.modelIds)
 foreach($taskModel in $RequiredModels){if($taskModelIDs -notcontains $taskModel){$taskModelIDs+=@($taskModel)}}
 $taskManifest.modelIds=$taskModelIDs
 $taskManagedKeys=@($taskManifest.apiKeys|Where-Object{$_.id -eq 'campus-worker'})
 if($taskManagedKeys.Count -ne 1){throw 'managed_api_key_ambiguous'}
 $taskManagedKey=$taskManagedKeys[0]
 $taskAllowed=@($taskManagedKey.allowedModels|Where-Object{$_})
 foreach($taskModel in $RequiredModels){
  if(@($taskManagedKey.excludedModels) -contains $taskModel){throw 'required_model_explicitly_excluded'}
  if($taskAllowed.Count -gt 0 -and $taskAllowed -notcontains $taskModel){$taskAllowed+=@($taskModel)}
 }
 if($taskAllowed.Count -gt 0){$taskManagedKey.allowedModels=$taskAllowed}
 # Keep the per-account concurrency limit; give queued requests time to find a slot.
 $taskManifest.accountConcurrencyWaitMs=120000
 $taskConfig=Get-Content -Raw -Encoding UTF8 -LiteralPath ($taskRoot+'\config.json')|ConvertFrom-Json
 $taskConfig.'max-retry-interval'=120
 $taskConfig.streaming.'stream-open-timeout-ms'=240000
 $taskStage='publish';$taskChanged=$true
 [IO.File]::Replace($taskCandidate,$taskMaintenance,($taskBackup+'\maintenance.atomic-before.exe'))
 Save-TaskJSON ($taskRoot+'\manifest.json') $taskManifest
 Save-TaskJSON ($taskRoot+'\config.json') $taskConfig
 $taskStage='verify_entitlements'
 $taskCatalogRaw=& $taskMaintenance -config C:\CodexStack\maintenance\config.json -action models
 if($LASTEXITCODE -ne 0){throw 'gateway_catalog_check_failed'}
 $taskCatalog=($taskCatalogRaw -join "`n")|ConvertFrom-Json
 if($taskCatalog.pending -gt 0){throw 'gateway_catalog_unresolved'}
 foreach($taskModel in $RequiredModels){if(@($taskCatalog.accounts|Where-Object{$_.state -in @('verified','verified_cached') -and @($_.available) -contains $taskModel}).Count -eq 0){throw 'required_model_not_supported'}}
 $taskStage='reload'
 & $taskCfg.account_reload_runner -Root $taskRoot -SkipCatalog
 if(!$?){throw 'gateway_reload_failed'}
 $taskPoolKey=[IO.File]::ReadAllText($taskRoot+'\client-key.txt').Trim()
 $taskModels=Invoke-RestMethod 'http://127.0.0.1:17442/v1/models' -Headers @{Authorization=('Bearer '+$taskPoolKey)} -TimeoutSec 10
 foreach($taskModel in $RequiredModels){if(@($taskModels.data|Where-Object{$_.id -eq $taskModel}).Count -eq 0){throw 'required_model_not_published'}}
 $taskReady=$true
 [pscustomobject]@{ok=$true;backup=$taskBackup;models=@($taskModels.data|Select-Object -ExpandProperty id);catalog=$taskCatalog;managed_key_allowed_models=$taskManagedKey.allowedModels;cooldown_wait_seconds=120;concurrency_wait_ms=120000;stream_open_timeout_ms=240000;maintenance_sha256=(Get-FileHash -LiteralPath $taskMaintenance).Hash.ToLower();pool_process_count=@(Get-Process|Where-Object{$_.Path -eq 'C:\CodexStack\cockpit-gateway\1.3.65\cockpit-cliproxy.exe'}).Count}|ConvertTo-Json -Depth 12 -Compress
}catch{
 $taskFailure=$_.Exception.Message
 if($taskChanged -and !$taskReady){
  foreach($taskPath in $taskOriginals.Keys){[IO.File]::WriteAllBytes($taskPath,$taskOriginals[$taskPath])}
  Copy-Item -LiteralPath ($taskBackup+'\maintenance.exe') -Destination $taskMaintenance -Force
  & $taskCfg.account_reload_runner -Root $taskRoot -SkipCatalog
 }
 [pscustomobject]@{ok=$false;stage=$taskStage;reason=$taskFailure;restored=$taskChanged}|ConvertTo-Json -Compress
 throw 'gateway_repair_failed'
}finally{
 if($taskLease){$null=Invoke-RestMethod ('http://'+$taskCfg.listen+'/maintenance/lease') -Method POST -Headers @{Authorization=('Bearer '+$taskRelayKey)} -ContentType 'application/json' -Body (@{Action='release';Lease=$taskLease.lease}|ConvertTo-Json) -TimeoutSec 10}
 if($taskLocked){$taskMutex.ReleaseMutex()};$taskMutex.Dispose()
}
