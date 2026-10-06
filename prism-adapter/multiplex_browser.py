"""UI-authored starts with bounded pages, then multiplexed browser polls.

No model start is synthesized or replayed: the official editor submits it once.
The first status body is captured before dispatch; subsequent polls use the
account's stationary page and its normal fetch implementation. Unknown outcomes
remain in the per-conversation journal.
"""
import asyncio
import hashlib
import json
import logging
import re
import time
import uuid
from collections import OrderedDict
from pathlib import Path
from urllib.parse import urlparse

from playwright.async_api import async_playwright

from multiplex_runtime import Admission, TurnJournal
from browser_gate import BrowserGate
from model_selection import select_options_async


RUNTIME_RATE_LIMIT = re.compile(
    r'项目运行环境的启动请求受到限流|(?:project|sandbox|runtime).{0,80}(?:startup|start).{0,80}rate.limit',
    re.I,
)


async def wait_for_editor(page, api):
    editor = page.locator('textarea[placeholder="Ask anything"]')
    blocked = page.get_by_text(RUNTIME_RATE_LIMIT).first
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        if await blocked.is_visible():
            raise api.AdapterError(429, 'project_runtime_rate_limited',
                'Prism project runtime startup is rate limited; wait before retrying; model request was not submitted')
        if await editor.is_visible():
            return editor
        await asyncio.sleep(0.25)
    raise api.AdapterError(503, 'project_editor_unavailable',
        'Prism project editor did not become ready; model request was not submitted')


POLL_JS = """async ({origin, path, body}) => {
  if (location.origin !== origin || typeof window.fetch !== 'function')
    return {status:0, error:'context_lost'};
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 30000);
  try {
    const response = await window.fetch(path, {method:'POST', credentials:'same-origin',
      headers:{'Content-Type':'application/json'}, body:JSON.stringify(body),
      redirect:'error', signal:controller.signal});
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let raw='', size=0;
    while (true) {
      const {done,value} = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > 2097152) { await reader.cancel(); return {status:0,error:'response_too_large'}; }
      raw += decoder.decode(value,{stream:true});
    }
    raw += decoder.decode();
    if (response.status !== 200) return {status:response.status};
    return {status:200,data:JSON.parse(raw)};
  } catch { return {status:0,error:'transport_failed'}; }
  finally { clearTimeout(timer); }
}"""


def fingerprint(body):
    return hashlib.sha256(json.dumps(body, sort_keys=True, separators=(",", ":")).encode()).digest()


def cgroup_memory_bytes():
    """Read this service's cgroup, never use machine-wide memory as its usage."""
    try:
        for line in Path('/proc/self/cgroup').read_text().splitlines():
            hierarchy, controllers, group = line.split(':', 2)
            if hierarchy == '0' and controllers == '':
                path = Path('/sys/fs/cgroup') / group.lstrip('/') / 'memory.current'
                return int(path.read_text().strip())
    except (OSError, ValueError):
        pass
    return None


class AccountBrowser:
    def __init__(self, engine, account_id, token):
        self.engine, self.api = engine, engine.api
        self.account_id = account_id
        self.credential = hashlib.sha256(token.encode()).digest()
        self.context = self.page = None
        self.refs = 0
        self.used = time.monotonic()
        self.created = self.used
        self.projects = OrderedDict()
        self.expected = {}

    async def open(self, browser, token):
        self.context = await browser.new_context(user_agent=self.api.USER_AGENT, service_workers='block')
        try:
            await self.context.add_cookies([{'name':'prism_oai_access_token', 'value':token,
                'domain':urlparse(self.api.BASE).hostname, 'path':'/', 'secure':self.api.BASE.startswith('https:')}])
            self.page = await self.context.new_page()
            self.page.set_default_timeout(60000)
            await self.page.add_init_script('window.__prismOriginalFetch = window.fetch;')
            self.gate = await BrowserGate.install(self.context, self.page, self.api.BASE, self.route)
            response = await self.page.goto(self.api.BASE, wait_until='domcontentloaded', timeout=60000)
            if not response or response.status != 200 or not self.page.url.startswith(self.api.BASE + '/'):
                raise self.api.AdapterError(503, 'poll_carrier_unavailable', 'Prism official polling page is unavailable')
            try:
                await self.page.wait_for_function("""() => window.__prismOriginalFetch &&
                    window.fetch !== window.__prismOriginalFetch && window.SentinelSDK &&
                    typeof window.SentinelSDK.token === 'function'""", timeout=30000)
            except Exception:
                raise self.api.AdapterError(503, 'poll_carrier_unavailable', 'Prism official fetch wrapper is not ready') from None
        except BaseException:
            await self.close()
            raise

    async def route(self, route):
        try:
            request = route.request
            if request.url == self.api.BASE + self.api.STATUS and request.method == 'POST':
                body = request.post_data_json
                grant = self.expected.get(fingerprint(body))
                if grant is not None and not grant['sent']:
                    grant['sent'] = True
                    await route.continue_()
                    return
        except Exception:
            pass
        try:
            await route.abort()
        except Exception:
            pass  # A preparation page may have closed after handing off.

    async def poll(self, body):
        key = fingerprint(body)
        if key in self.expected:
            raise self.api.AdapterError(502, 'duplicate_poll', 'Prism poll identity was reused')
        grant = {'sent':False}
        self.expected[key] = grant
        try:
            result = await self.page.evaluate(POLL_JS, {'origin':self.api.BASE, 'path':self.api.STATUS, 'body':body})
            if not grant['sent'] or not isinstance(result, dict) or result.get('status') != 200:
                self.engine.observe('prism_poll_failed', sent=grant['sent'],
                    http_status=result.get('status') if isinstance(result, dict) else None,
                    transport_error=result.get('error') if isinstance(result, dict) else 'invalid_result')
                raise self.api.AdapterError(502, 'poll_failed', 'Prism poll failed; pending state retained')
            data = result.get('data')
            if not isinstance(data, dict):
                raise self.api.AdapterError(502, 'invalid_response', 'Prism poll returned an invalid response')
            return data
        finally:
            self.expected.pop(key, None)

    async def create_project(self):
        # This UUID is the official projects API's client-assigned idempotency
        # key. Never use it for model metadata unless the server confirms it.
        project = str(uuid.uuid4())
        result = await self.page.evaluate(POLL_JS, {'origin':self.api.BASE, 'path':'/api/projects',
            'body':{'project_uuid':project, 'title':'Untitled'}})
        if isinstance(result, dict) and result.get('status') == 429:
            raise self.api.AdapterError(429, 'project_runtime_rate_limited',
                'Prism project creation is rate limited; wait before retrying; model request was not submitted')
        data = result.get('data') if isinstance(result, dict) else None
        if (not isinstance(result, dict) or result.get('status') != 200
                or not isinstance(data, dict) or data.get('uuid') != project):
            raise self.api.AdapterError(502, 'project_creation_failed', 'Prism did not confirm the new project; model request was not submitted')
        return project

    async def close(self):
        context, self.context = self.context, None
        self.page = None
        self.projects.clear()
        if context is not None:
            await context.close()


class BrowserStart:
    """A single editor page exists only through start and the first poll body."""
    def __init__(self, engine, actor, journal, session_id, model=None, effort='medium'):
        self.engine, self.actor, self.journal, self.api = engine, actor, journal, engine.api
        self.session_id = session_id
        self.model, self.effort = model if model is not None else self.api.MODEL, effort
        self.page = None
        self.project = None
        self.armed = self.sent = self.closed = False
        self.start_request = None
        self.start_fingerprint = None
        self.violation = False
        self.request_id = ''
        self.start_response = asyncio.get_running_loop().create_future()
        self.poll_body = asyncio.get_running_loop().create_future()
        self.cache_hit = False
        self.phase = 'initializing'
        self.start_attempts = 0
        self.intent = None
        self.reconnects = 0

    async def route(self, route):
        try:
            request = route.request
            body = request.post_data_json
            if request.url == self.api.BASE + self.api.START:
                metadata = body.get('metadata') if isinstance(body, dict) else None
                if (not self.closed and self.armed and not self.sent and isinstance(metadata, dict)
                        and metadata.get('model') == self.model and metadata.get('reasoning_effort') == self.effort
                        and metadata.get('projectId') == self.project and request.method == 'POST'):
                    intent = fingerprint({key:body.get(key) for key in ('input', 'previousResponseId', 'conversationId')})
                    if self.intent is not None and intent != self.intent:
                        self.violation = True
                        await route.abort()
                        return
                    self.intent = intent
                    self.start_attempts += 1
                    self.sent = True
                    self.start_request = request
                    self.start_fingerprint = fingerprint(body)
                    await route.continue_()
                    return
                self.violation = True
            elif request.url == self.api.BASE + self.api.STATUS and self.sent and not self.closed:
                # The response callback may still be decoding JSON when this
                # route fires. Await that callback rather than trusting the page ID.
                await asyncio.wait_for(asyncio.shield(self.start_response), timeout=30)
                if (isinstance(body, dict) and self.request_id and body.get('request_id') == self.request_id
                        and not self.poll_body.done()):
                    self.poll_body.set_result(json.loads(json.dumps(body)))
        except Exception:
            self.violation = True
        try:
            await route.abort()
        except Exception:
            pass

    async def response(self, response):
        if response.url != self.api.BASE + self.api.START or self.start_fingerprint is None or self.start_response.done():
            return
        try:
            if fingerprint(response.request.post_data_json) != self.start_fingerprint:
                return
            data = await response.json()
            if response.status != 200 or not isinstance(data, dict):
                self.start_response.set_result(None)
                return
            request_id = data.get('request_id')
            if not isinstance(request_id, str) or not request_id or len(request_id) > 1024:
                self.start_response.set_result(None)
                return
            if (data.get('status') == 'completed'
                    and isinstance(data.get('response'), dict)
                    and data['response'].get('status') == 'error'
                    and self.api.terminal_failure_reason(data) == 'sandbox_reconnecting'
                    and self.start_attempts < 3):
                # The official UI waits for ensureSandboxConnection then authors
                # another start. Only this explicit pre-execution result permits
                # rearming; never synthesize a retry or repeat an unknown start.
                self.reconnects += 1
                self.journal.update(stage='runtime_reconnecting', request_id=request_id)
                self.sent = False
                self.start_fingerprint = None
                self.phase = 'waiting_runtime_reconnect'
                self.engine.observe('prism_runtime_reconnect_wait', self.journal,
                                    attempts=self.start_attempts)
                return
            self.request_id = request_id
            self.start_response.set_result(data)
        except Exception:
            if not self.start_response.done():
                self.start_response.set_result(None)

    async def run(self, prompt):
        self.phase = 'opening_project_page'
        page = self.page = await self.actor.context.new_page()
        page.set_default_timeout(60000)
        self.gate = await BrowserGate.install(self.actor.context, page, self.api.BASE, self.route)
        page.on('response', self.response)
        entry = self.actor.projects.get(self.session_id) if self.session_id else None
        if entry and time.monotonic() - entry[1] < 900:
            self.project, _ = entry
            self.cache_hit = True
            await page.goto(self.api.BASE + '/?u=' + self.project + '&pg=1', wait_until='domcontentloaded', timeout=60000)
            self.phase = 'opening_fresh_chat'
            await page.get_by_role('button', name='New chat tab', exact=True).click(timeout=60000)
        else:
            self.phase = 'creating_project'
            self.project = await self.actor.create_project()
            self.phase = 'loading_project'
            await page.goto(self.api.BASE + '/?u=' + self.project + '&pg=1', wait_until='domcontentloaded', timeout=60000)
        self.phase = 'waiting_editor'
        textarea = await wait_for_editor(page, self.api)
        self.phase = 'selecting_model_and_effort'
        await select_options_async(page, self.model, self.effort, self.api.AdapterError)
        self.phase = 'submitting'
        await textarea.fill(prompt)
        self.journal.update(stage='submitting', project_id=self.project, model=self.model, reasoning_effort=self.effort)
        self.armed = True
        await textarea.press('Enter')
        self.phase = 'awaiting_start'
        data = await asyncio.wait_for(asyncio.shield(self.start_response), timeout=90)
        if data is None or self.violation:
            raise self.api.AdapterError(502, 'invalid_start', 'Prism start did not return a valid identity; inspect pending state')
        self.journal.update(stage='polling', request_id=self.request_id, turn_state=data.get('turn_state'))
        if self.api.terminal_text(data) is not None:
            return data, None
        self.phase = 'awaiting_poll_handoff'
        body = await asyncio.wait_for(asyncio.shield(self.poll_body), timeout=30)
        if self.violation:
            raise self.api.AdapterError(502, 'unexpected_start', 'Prism attempted an unexpected model start')
        return data, body

    async def close(self):
        self.closed = True
        if self.page is not None:
            page, self.page = self.page, None
            page.remove_listener('response', self.response)
            await page.close(run_before_unload=False)


class MultiplexBrowser:
    def __init__(self, state, chrome, api, active=20, per_account=20, queued=30,
                 wait_seconds=15, bootstrap=1, accounts=1, idle_seconds=300, poll_seconds=2):
        if not 1 <= bootstrap <= 2 or not 1 <= accounts <= 2 or not 30 <= idle_seconds <= 900:
            raise ValueError('invalid Prism browser limits')
        self.state, self.chrome, self.api = state, chrome, api
        self.admission = Admission(api, active, per_account, queued, wait_seconds)
        self.bootstrap = asyncio.Semaphore(bootstrap)
        self.max_accounts, self.idle_seconds, self.poll_seconds = accounts, idle_seconds, poll_seconds
        self.actors = {}
        self.actor_lock = asyncio.Lock()
        self.playwright = self.browser = None
        self.polling = 0
        self.preparing = 0
        self.memory_wait_seconds = 30
        self.runtime_cooldowns = {}

    def observe(self, event, journal=None, **fields):
        # Only explicit operational fields; never tokens, URLs, prompts or turn state.
        record = {'event': event, 'active': self.admission.running,
                  'queued': self.admission.outstanding - self.admission.running,
                  'preparing': self.preparing, 'polling': self.polling,
                  'memory_bytes': cgroup_memory_bytes()}
        if journal is not None:
            record['local_id'] = journal.local_id
        record.update(fields)
        logging.getLogger('prism.lifecycle').warning(json.dumps(record, separators=(',', ':')))

    async def collect_closed_pages(self, actor):
        # Same-origin preparation pages may share a renderer with the carrier.
        # Collect detached editor contexts while keeping live polls and fetch intact.
        gate = getattr(actor, 'gate', None)
        if gate is not None:
            try:
                await asyncio.wait_for(gate.session.send('HeapProfiler.collectGarbage'), timeout=5)
            except Exception:
                self.observe('prism_collection_unavailable')

    async def wait_for_memory(self, actor, journal):
        deadline = time.monotonic() + self.memory_wait_seconds
        collected = False
        waiting = False
        while True:
            memory = cgroup_memory_bytes()
            if memory is None or memory < 750 * 1024 * 1024:
                if waiting:
                    self.observe('prism_memory_ready', journal)
                return
            if not waiting:
                waiting = True
                self.observe('prism_memory_wait', journal)
            if not collected:
                collected = True
                await self.collect_closed_pages(actor)
                continue
            if time.monotonic() >= deadline:
                self.observe('prism_memory_rejected', journal)
                raise self.api.AdapterError(429, 'resource_pressure',
                    'Prism memory budget did not recover; request was not submitted')
            await asyncio.sleep(min(0.25, max(0, deadline - time.monotonic())))

    async def _browser(self):
        if self.browser is None:
            self.playwright = await async_playwright().start()
            try:
                self.browser = await self.playwright.chromium.launch(executable_path=self.chrome, headless=True, chromium_sandbox=True)
            except BaseException:
                await self.playwright.stop()
                self.playwright = None
                raise
        return self.browser

    async def account(self, account_id, token):
        async with self.actor_lock:
            actor = self.actors.get(account_id)
            if actor is not None and actor.credential != hashlib.sha256(token.encode()).digest():
                if actor.refs:
                    raise self.api.AdapterError(429, 'credential_rotation', 'Old Prism credential is draining; request was not submitted')
                self.actors.pop(account_id)
                await actor.close()
                actor = None
            if actor is None:
                if len(self.actors) >= self.max_accounts:
                    idle = [a for a in self.actors.values() if not a.refs]
                    if not idle:
                        raise self.api.AdapterError(429, 'prism_busy', 'Prism account contexts are busy; request was not submitted')
                    victim = min(idle, key=lambda a:a.used)
                    self.actors.pop(victim.account_id)
                    await victim.close()
                actor = AccountBrowser(self, account_id, token)
                await actor.open(await self._browser(), token)
                self.actors[account_id] = actor
            actor.refs += 1
            return actor

    async def run(self, account_id, token, prompt, session_id=None, model=None, effort='medium', reuse_project=True):
        model = self.api.MODEL if model is None else model
        async with self.admission.enter(account_id, session_id):
            journal = TurnJournal(self.state, self.api, account_id, session_id)
            actor = start = None
            succeeded = False
            try:
                journal.begin()
                if time.monotonic() < self.runtime_cooldowns.get(account_id, 0):
                    raise self.api.AdapterError(429, 'project_runtime_rate_limited',
                        'Prism runtime startup is cooling down; model request was not submitted')
                actor = await self.account(account_id, token)
                start = BrowserStart(self, actor, journal, session_id if reuse_project else None, model, effort)
                async with self.bootstrap:
                    if time.monotonic() < self.runtime_cooldowns.get(account_id, 0):
                        raise self.api.AdapterError(429, 'project_runtime_rate_limited',
                            'Prism runtime startup is cooling down; model request was not submitted')
                    await self.wait_for_memory(actor, journal)
                    self.preparing += 1
                    self.observe('prism_prepare_start', journal, model=model, effort=effort)
                    try:
                        try:
                            data, body = await start.run(prompt)
                        except self.api.AdapterError:
                            raise
                        except Exception:
                            disposition = 'inspect pending state' if start.sent else 'model request was not submitted'
                            raise self.api.AdapterError(502, 'preparation_failed',
                                'Prism preparation failed at ' + start.phase + '; ' + disposition) from None
                    finally:
                        try:
                            await start.close()
                            await self.collect_closed_pages(actor)
                        finally:
                            self.preparing -= 1
                            self.observe('prism_prepare_end', journal, submitted=start.sent)
                    if start.violation:
                        raise self.api.AdapterError(502, 'unexpected_start', 'Prism attempted an unexpected model start')
                needs_poll = self.api.terminal_text(data) is None
                if needs_poll:
                    self.polling += 1
                    self.observe('prism_poll_start', journal, model=model, effort=effort)
                try:
                    polls = 0
                    while self.api.terminal_text(data) is None:
                        # Only the trusted response may rotate the opaque turn state.
                        if isinstance(data.get('turn_state'), (str, dict)) and data['turn_state']:
                            body['turn_state'] = data['turn_state']
                        data = await actor.poll(body)
                        polls += 1
                        if data.get('request_id') not in (None, '', start.request_id):
                            raise self.api.AdapterError(502, 'foreign_response', 'Prism returned a different request identity')
                        journal.update(stage='polling', request_id=start.request_id, turn_state=data.get('turn_state', body.get('turn_state')))
                        if self.api.terminal_text(data) is None:
                            jitter = int(journal.local_id[:2], 16) / 255 * 0.25
                            await asyncio.sleep(self.poll_seconds + jitter)
                finally:
                    if needs_poll:
                        self.polling -= 1
                        self.observe('prism_poll_end', journal)
                result = self.api.terminal_text(data)
                self.state.receipt(account_id, start.request_id, getattr(start, 'start_attempts', 1), polls, result, start.cache_hit, model=model, effort=effort)
                journal.finish()
                if isinstance(result, self.api.AdapterError):
                    response = data.get('response') if isinstance(data.get('response'), dict) else {}
                    self.observe('prism_upstream_terminal_failure', journal,
                        response_failed=response.get('status') in ('failed', 'error'),
                        turn_failed=data.get('status') in ('failed', 'error'),
                        from_start=polls == 0,
                        reason=self.api.terminal_failure_reason(data),
                        **self.api.terminal_failure_diagnostics(data))
                    raise result
                if session_id and reuse_project:
                    actor.projects[session_id] = (start.project, time.monotonic())
                    actor.projects.move_to_end(session_id)
                    while len(actor.projects) > 128:
                        actor.projects.popitem(last=False)
                succeeded = True
                self.observe('prism_turn_complete', journal, model=model, effort=effort, polls=polls)
                return start.request_id, result
            except Exception as error:
                if getattr(error, 'code', None) == 'project_runtime_rate_limited' and time.monotonic() >= self.runtime_cooldowns.get(account_id, 0):
                    self.runtime_cooldowns[account_id] = time.monotonic() + 60
                self.observe('prism_turn_error', journal,
                    code=getattr(error, 'code', type(error).__name__),
                    phase=getattr(start, 'phase', 'account_preparation'),
                    submitted=start is not None and start.sent)
                error.not_submitted = start is None or not start.sent
                raise
            finally:
                try:
                    if start is not None:
                        await start.close()
                finally:
                    try:
                        if (start is None or not start.sent) and journal.owned:
                            journal.finish()
                    finally:
                        if actor is not None:
                            if not succeeded and session_id:
                                actor.projects.pop(session_id, None)
                            actor.refs -= 1
                            actor.used = time.monotonic()

    async def prune(self):
        async with self.actor_lock:
            now = time.monotonic()
            self.runtime_cooldowns = {key: expiry for key, expiry in self.runtime_cooldowns.items() if expiry > now}
            memory = cgroup_memory_bytes()
            pressure = memory is not None and memory >= 750 * 1024 * 1024
            for key, actor in list(self.actors.items()):
                if not actor.refs and (pressure or now - actor.used >= self.idle_seconds or now - actor.created >= 900):
                    self.actors.pop(key)
                    await actor.close()
            if not self.actors:
                await self.close_browser()

    async def close_browser(self):
        try:
            if self.browser is not None:
                await self.browser.close()
        finally:
            self.browser = None
            if self.playwright is not None:
                await self.playwright.stop()
                self.playwright = None

    async def close(self):
        self.admission.closed = True
        for actor in list(self.actors.values()):
            await actor.close()
        self.actors.clear()
        await self.close_browser()
