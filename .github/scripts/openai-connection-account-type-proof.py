"""Validate behavioral RED/GREEN from remote test reporters, never exit code alone."""
import json
import sys
from pathlib import Path

mode, directory = sys.argv[1:]
root = Path(directory)


def go_events(filename):
    events = []
    for line in (root / filename).read_text().splitlines():
        if line.startswith('{'):
            events.append(json.loads(line))
    assert events, f'No Go test events: {filename}'
    output = ''.join(e.get('Output', '') for e in events)
    assert '[build failed]' not in output, output
    assert not any(e.get('Action') == 'skip' for e in events), 'Skipped tests'
    return events


def require_case(events, action, name):
    assert any(e.get('Action') == action and e.get('Test') == name for e in events), (action, name)


if mode == 'backend-red':
    service = go_events('service.jsonl')
    handler = go_events('handler.jsonl')
    for case in ['TestOpenAIConnectionManagementRejectsAPIKey/list',
                 'TestOpenAIConnectionCreationRejectsMixedCredentials/api_key']:
        require_case(service, 'fail', case)
    for case in ['TestOpenAIConnectionHandlerAccountType/apikey/list',
                 'TestOpenAIConnectionHandlerAccountType/oauth/reject_mixed']:
        require_case(handler, 'fail', case)
    for events, message in [(service, 'management must reject account.type=apikey'),
                            (service, 'OAuth registration must reject API key credentials'),
                            (handler, 'API key management must be refused'),
                            (handler, 'OAuth parent must reject API key creation')]:
        assert message in ''.join(e.get('Output', '') for e in events), message
elif mode in ['backend-green', 'integration-green']:
    events = go_events('backend.jsonl' if mode == 'backend-green' else 'integration.jsonl')
    assert not any(e.get('Action') == 'fail' for e in events), 'Failed tests'
    for case in ['TestOpenAIConnectionManagementRejectsAPIKey',
                 'TestOpenAIConnectionCreationRejectsMixedCredentials',
                 'TestOpenAIConnectionOAuthKindsRemainAvailable',
                 'TestOpenAIConnectionHistoricalAPIKeyRuntime',
                 'TestOpenAIConnectionOrdinaryAPIKeyRouteUnchanged']:
        require_case(events, 'pass', case)
    if mode == 'backend-green':
        require_case(events, 'pass', 'TestOpenAIConnectionHandlerAccountType')
    else:
        for case in ['TestIssue594HTTPDatabaseCoexistenceRotationUnlinkAndLegacy',
                     'TestIssue594ProductionGatewayConnectionRouteAndAccountSafety',
                     'TestIssue594ReviewRealHTTPSelectionSlotsOutputAndUsage',
                     'TestIssue594SIWCFinalEgressFencesRevocationAndRevision']:
            require_case(events, 'pass', case)
elif mode in ['ui-red', 'ui-green']:
    report = json.loads((root / 'ui.json').read_text())
    cases = [case for file in report['testResults'] for case in file['assertionResults']]
    titles = [
        'hides the whole panel and makes no requests for API key accounts across watches',
        'offers only Codex BPS SIWC and sends no API key in OAuth creation',
        'never mounts or fetches connections for API key accounts, including type and dialog changes',
    ]
    expected = 'failed' if mode == 'ui-red' else 'passed'
    for title in titles:
        assert any(c['title'] == title and c['status'] == expected for c in cases), (expected, title)
    if mode == 'ui-green':
        assert cases and all(c['status'] == 'passed' for c in cases), 'Failed/skipped/todo UI test'
    else:
        log = (root / 'ui.log').read_text()
        assert not any(s in log for s in ['Failed to resolve', 'SyntaxError', 'No test files found']), log
else:
    raise AssertionError(f'Unknown proof mode: {mode}')
print(f'{mode}: required behavioral assertions verified')
