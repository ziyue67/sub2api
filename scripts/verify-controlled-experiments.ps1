param([string]$BaseUrl = 'http://127.0.0.1:18085', [Parameter(Mandatory)][string]$AdminPassword, [string]$ReportPath)
$ErrorActionPreference = 'Stop'
if (([uri]$BaseUrl).Host -notin @('127.0.0.1','localhost')) { throw 'This verifier is for the isolated loopback staging environment only' }
$login = Invoke-RestMethod -Uri "$BaseUrl/api/v1/auth/login" -Method Post -ContentType 'application/json' -Body (@{email='experiment-admin@example.test';password=$AdminPassword} | ConvertTo-Json)
$headers = @{ Authorization = 'Bearer '+$login.data.access_token }
if (-not $login.data.access_token) { throw 'Staging administrator login failed' }
function Request-Admin([string]$Path, $Body = $null, [string]$Method = 'Get') {
    $parameters = @{ Uri="$BaseUrl/api/v1/admin$Path"; Headers=$headers; Method=$Method }
    if ($null -ne $Body) { $parameters.ContentType='application/json'; $parameters.Body=$Body | ConvertTo-Json -Depth 20 }
    return (Invoke-RestMethod @parameters).data
}
function Await-Run([long]$Id) {
    $deadline = (Get-Date).AddSeconds(45)
    do {
        $report = Request-Admin "/controlled-experiments/$Id"
        if ($report.run.status -notin @('running','stop_requested')) { return $report }
        Start-Sleep -Milliseconds 300
    } while ((Get-Date) -lt $deadline)
    throw 'Staging experiment did not finish'
}
function Assert-Check([bool]$Condition, [string]$Message) { if (-not $Condition) { throw $Message } }
$catalog = Request-Admin '/controlled-experiments/catalog'
Assert-Check ($catalog.tasks.Count -eq 44) 'Expected the complete 44-task bank'
$accounts = @()
foreach ($name in @('fixture-a','fixture-b','fixture-incomplete')) {
    $accounts += Request-Admin '/accounts' @{name=$name;platform='openai';type='apikey';credentials=@{api_key=$name;base_url='http://fixture:8087';model_provider='openai'};extra=@{openai_responses_mode='force_responses'};concurrency=1;priority=1} 'Post'
}
$input = @{name='Synthetic paired answer and tool check';model='gpt-5.4';reasoning_effort='high';split='screen';repetitions=1;max_calls=8;timeout_seconds=30;task_ids=@('screen-constraints-01','screen-tools-01');routes=@(@{account_id=$accounts[0].id;channel='native_http'},@{account_id=$accounts[1].id;channel='native_http'})}
$draft = Request-Admin '/controlled-experiments' $input 'Post'
Assert-Check ($draft.status -eq 'draft' -and $draft.reserved_calls -eq 0) 'Saving must not generate requests'
$null = Request-Admin "/controlled-experiments/$($draft.id)/start" @{} 'Post'
$paired = Await-Run $draft.id
Assert-Check ($paired.run.status -eq 'completed' -and $paired.run.reserved_calls -eq 8) 'Expected exactly eight persisted reservations'
Assert-Check ($paired.comparisons[0].paired_tasks -eq 2 -and $paired.comparisons[0].mean_difference -eq 0) 'Expected two equally scored strict pairs'
foreach ($route in $paired.routes) { Assert-Check ($route.eligibility -eq 'verified' -and $route.graded_tasks -eq 2 -and $route.passed_tasks -eq 2 -and -not $route.cost_incomplete) 'Expected qualification, scoring and complete cost accounting' }
Assert-Check (@($paired.attempts | Where-Object { $_.tool_trace.Count -gt 0 }).Count -gt 0) 'Tool continuation must preserve local evidence'
Assert-Check (-not (($paired | ConvertTo-Json -Depth 60) -match 'fixture-opaque-reasoning')) 'Opaque reasoning must not be persisted'
$input.name='Synthetic exhausted budget'; $input.max_calls=1; $input.task_ids=@('screen-constraints-01'); $input.routes=@(@{account_id=$accounts[0].id;channel='native_http'})
$limited = Request-Admin '/controlled-experiments' $input 'Post'
$null = Request-Admin "/controlled-experiments/$($limited.id)/start" @{} 'Post'
$budget = Await-Run $limited.id
Assert-Check ($budget.run.status -eq 'budget_exhausted' -and $budget.run.reserved_calls -eq 1 -and $budget.attempts.Count -eq 1) 'Submission budget must stop before a second send'
$input.name='Synthetic incomplete protocol'; $input.max_calls=2; $input.routes=@(@{account_id=$accounts[2].id;channel='native_http'})
$broken = Request-Admin '/controlled-experiments' $input 'Post'
$null = Request-Admin "/controlled-experiments/$($broken.id)/start" @{} 'Post'
$unknown = Await-Run $broken.id
Assert-Check ($unknown.run.reserved_calls -eq 1 -and $unknown.routes[0].graded_tasks -eq 0 -and $unknown.routes[0].unknown_calls -eq 1) 'Incomplete qualification must remain unknown, unscored and unreplayed'
$evidence = @{verified_at=(Get-Date).ToString('o');synthetic_upstream=$true;real_model_submissions=0;catalog_tasks=$catalog.tasks.Count;paired=$paired;budget=$budget;unknown=$unknown}
if ($ReportPath) { $evidence | ConvertTo-Json -Depth 60 | Set-Content -LiteralPath $ReportPath -Encoding utf8 }
[pscustomobject]@{tasks=$catalog.tasks.Count;paired_run_id=$paired.run.id;paired_calls=$paired.run.reserved_calls;paired_tasks=$paired.comparisons[0].paired_tasks;budget_status=$budget.run.status;unknown_calls=$unknown.routes[0].unknown_calls;real_model_submissions=0}
