$ErrorActionPreference='Stop'
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'windows-maintenance.ps1'),[ref]$tokens,[ref]$errors)
if($errors.Count){throw 'runner_parse_failed'}
$definition=$ast.Find({param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq 'Check-Accounts'},$true)
Invoke-Expression $definition.Extent.Text
$taskRoot=Join-Path ([IO.Path]::GetTempPath()) ('wechat-maintenance-test-'+[guid]::NewGuid().ToString('N'))
$null=New-Item -ItemType Directory -Path $taskRoot
$script:calls=@();$script:pending=2;$script:busy=$false;$script:catalogFailure=$false
function Lease($Action,$ID){if($script:busy){return $null};return @{lease='test-lease'}}
function Test-Idle{return $true}
function Restart-OwnedTask($Name,$Binary){$script:calls+='restart'}
function Wait-ManagedHealth($Name,$Binary){return $true}
function Invoke-Maintenance($Action,$Extra){
 $script:calls+=$Action
 if($Action -eq 'models' -and $script:catalogFailure){throw 'catalog_unavailable'}
 if($Action -eq 'accounts'){
  $day=[TimeZoneInfo]::ConvertTimeBySystemTimeZoneId([DateTime]::UtcNow,'China Standard Time').ToString('yyyy-MM-dd')
  [IO.File]::WriteAllText((Join-Path $taskRoot ('accounts-'+$day+'.json')),'{"reload_required":true,"accounts":[]}')
 }
 return @{pending=$script:pending;reload_required=$false}
}
function Assert-Calls([string]$Expected){if(($script:calls -join ',') -ne $Expected){throw ('unexpected_actions_'+($script:calls -join ','))};$script:calls=@()}
try {
 $day=[TimeZoneInfo]::ConvertTimeBySystemTimeZoneId([DateTime]::UtcNow,'China Standard Time').ToString('yyyy-MM-dd')
 $summary=Join-Path $taskRoot ('accounts-'+$day+'.json')
 $applied=Join-Path $taskRoot ('accounts-applied-'+$day+'.flag')
 [IO.File]::WriteAllText($summary,'{"reload_required":true,"accounts":[]}')
 [IO.File]::WriteAllText($applied,'1')
 Check-Accounts -ResumeOnly;Assert-Calls 'models'
 Check-Accounts -ResumeOnly;Assert-Calls 'models'
 if(!(Test-Path -LiteralPath (Join-Path $taskRoot 'accounts-pending.flag'))){throw 'lost_catalog_retry'}
 $script:pending=0
 Check-Accounts -ResumeOnly;Assert-Calls 'models'
 if(Test-Path -LiteralPath (Join-Path $taskRoot 'accounts-pending.flag')){throw 'completed_catalog_still_pending'}
 Remove-Item -LiteralPath $applied
 $script:catalogFailure=$true
 try{Check-Accounts -ResumeOnly;throw 'expected_catalog_failure'}catch{if($_.Exception.Message -ne 'catalog_unavailable'){throw}}
 Assert-Calls 'models'
 if(Test-Path -LiteralPath $applied){throw 'failed_reload_marked_applied'}
 $script:catalogFailure=$false
 Check-Accounts -ResumeOnly;Assert-Calls 'models,restart'
 Check-Accounts -ResumeOnly;Assert-Calls 'models'
 $script:catalogFailure=$true
 try{Check-Accounts;throw 'expected_catalog_failure'}catch{if($_.Exception.Message -ne 'catalog_unavailable'){throw}}
 Assert-Calls 'accounts,models'
 if(Test-Path -LiteralPath $applied){throw 'previous_reload_marker_survived_new_check'}
 if(!(Test-Path -LiteralPath (Join-Path $taskRoot 'accounts-pending.flag'))){throw 'failed_catalog_retry_not_queued'}
 $script:catalogFailure=$false
 Check-Accounts -ResumeOnly;Assert-Calls 'models,restart'
 Check-Accounts -ResumeOnly;Assert-Calls 'models'
 Remove-Item -LiteralPath $summary
 Check-Accounts -ResumeOnly;Assert-Calls 'accounts,models,restart'
 Check-Accounts;Assert-Calls 'accounts,models,restart'
 $script:busy=$true
 Check-Accounts -ResumeOnly;Assert-Calls ''
 if(!(Test-Path -LiteralPath (Join-Path $taskRoot 'accounts-pending.flag'))){throw 'lost_busy_account_retry'}
 Write-Output '{"ok":true,"catalog_retry_does_not_repeat_accounts":true,"interrupted_credential_reload_resumed_once":true,"missing_checkpoint_retried":true,"daily_check_preserved":true,"busy_retry_preserved":true}'
}finally{
 foreach($file in @($summary,$applied,(Join-Path $taskRoot 'accounts-pending.flag'))){if(Test-Path -LiteralPath $file){Remove-Item -LiteralPath $file -Force}}
 Remove-Item -LiteralPath $taskRoot
}
