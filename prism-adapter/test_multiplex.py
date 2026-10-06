import asyncio
import json
import tempfile
import threading
import unittest
from collections import OrderedDict
from concurrent.futures import ThreadPoolExecutor
from unittest import mock
from types import SimpleNamespace

from test_server import adapter
from multiplex_runtime import Admission, AsyncBrowserWorker, TurnJournal
from browser_gate import BrowserGate
import multiplex_browser


class GateTests(unittest.IsolatedAsyncioTestCase):
    async def test_unhandled_interception_fails_closed(self):
        session = SimpleNamespace(send=mock.AsyncMock())
        handler = mock.AsyncMock(side_effect=ValueError('fixture'))
        gate = BrowserGate(session, handler)
        await gate.paused({'requestId':'paused-1','request':{'url':adapter.BASE+adapter.START,
            'method':'POST','postData':'{}'}})
        session.send.assert_awaited_once_with('Fetch.failRequest', {'requestId':'paused-1','errorReason':'BlockedByClient'})

    async def test_large_post_data_is_read_without_rewriting_or_double_dispatch(self):
        session = SimpleNamespace(send=mock.AsyncMock(return_value={'postData':'{"request_id":"fixture"}'}))
        async def handler(route):
            self.assertEqual(route.request.post_data_json, {'request_id':'fixture'})
            await route.continue_()
            await route.continue_()
        gate = BrowserGate(session, handler)
        await gate.paused({'requestId':'paused-1','networkId':'network-1',
            'request':{'url':adapter.BASE+adapter.STATUS,'method':'POST','hasPostData':True}})
        self.assertEqual(session.send.await_args_list, [
            mock.call('Network.getRequestPostData', {'requestId':'network-1'}),
            mock.call('Fetch.continueRequest', {'requestId':'paused-1'})])


class JournalTests(unittest.TestCase):
    def test_parallel_turns_are_isolated_and_old_adapter_cannot_ignore_them(self):
        with tempfile.TemporaryDirectory() as directory:
            state = adapter.State(directory)
            first = TurnJournal(state, adapter, '300', 'a' * 64)
            other = TurnJournal(state, adapter, '300', 'b' * 64)
            first.begin()
            other.begin()
            first.update(stage='polling', request_id='request-a', turn_state={'opaque':'first'})
            other.update(stage='polling', request_id='request-b', turn_state={'opaque':'second'})
            with self.assertRaises(adapter.AdapterError):
                TurnJournal(state, adapter, '300', 'a' * 64).begin()
            with self.assertRaises(adapter.AdapterError):
                state.ensure_idle('300')
            self.assertEqual(json.loads(first.path.read_text())['turn_state'], {'opaque':'first'})
            first.finish()
            self.assertTrue(other.path.exists())
            self.assertEqual(other.path.stat().st_mode & 0o777, 0o600)
            other.finish()
            state.ensure_idle('300')

    def test_legacy_pending_blocks_new_executor_and_stateless_tasks_are_distinct(self):
        with tempfile.TemporaryDirectory() as directory:
            state = adapter.State(directory)
            state.begin('300')
            with self.assertRaises(adapter.AdapterError) as blocked:
                TurnJournal(state, adapter, '300', None).begin()
            self.assertEqual(blocked.exception.status, 409)
            state.finish('300')
            first = TurnJournal(state, adapter, '300', None)
            second = TurnJournal(state, adapter, '300', None)
            first.begin()
            second.begin()
            self.assertNotEqual(first.path, second.path)
            first.finish()
            second.finish()


class AdmissionTests(unittest.IsolatedAsyncioTestCase):
    async def test_twenty_active_ten_queued_and_overflow_rejected_before_execution(self):
        gate = Admission(adapter, active=20, per_account=20, queued=10)
        release, started = asyncio.Event(), asyncio.Event()
        count = peak = completed = 0
        async def run(index):
            nonlocal count, peak, completed
            async with gate.enter('300', str(index)):
                count += 1
                peak = max(peak, count)
                if count == 20:
                    started.set()
                await release.wait()
                completed += 1
                count -= 1
        jobs = [asyncio.create_task(run(i)) for i in range(30)]
        try:
            await asyncio.wait_for(started.wait(), 2)
            self.assertEqual(gate.outstanding, 30)
            self.assertEqual(gate.running, 20)
            with self.assertRaises(adapter.AdapterError) as full:
                async with gate.enter('300', 'overflow'):
                    self.fail('overflow executed')
            self.assertEqual(full.exception.status, 429)
        finally:
            release.set()
            await asyncio.gather(*jobs)
        self.assertEqual((completed, peak, gate.running, gate.outstanding), (30, 20, 0, 0))
        self.assertEqual((gate.accounts, gate.scopes), ({}, {}))

    async def test_same_conversation_wait_does_not_reserve_other_slots(self):
        gate = Admission(adapter, active=2, per_account=2, queued=2)
        release, entered = asyncio.Event(), asyncio.Event()
        async def holder():
            async with gate.enter('300', 'same'):
                entered.set()
                await release.wait()
        task = asyncio.create_task(holder())
        await entered.wait()
        waiting = asyncio.create_task(holder())
        await asyncio.sleep(0)
        try:
            async with asyncio.timeout(1):
                async with gate.enter('300', 'other'):
                    self.assertEqual(gate.running, 2)
            waiting.cancel()
            await asyncio.gather(waiting, return_exceptions=True)
        finally:
            release.set()
            await task
        self.assertEqual(gate.outstanding, 0)
        self.assertFalse(gate.scopes)

    async def test_queue_timeout_releases_per_account_reservations(self):
        gate = Admission(adapter, active=1, per_account=1, queued=1, wait_seconds=0.01)
        async with gate.enter('300', 'first'):
            with self.assertRaises(adapter.AdapterError) as timed:
                async with gate.enter('300', 'second'):
                    self.fail('timed-out request executed')
            self.assertEqual(timed.exception.status, 429)
            self.assertEqual(gate.outstanding, 1)
        self.assertEqual(gate.outstanding, 0)


class WorkerTests(unittest.TestCase):
    def test_thirty_calls_share_one_browser_thread_without_serializing_turns(self):
        owners = set()
        class Engine:
            def __init__(self):
                self.count = 0
                self.ready = asyncio.Event()
            async def run(self, account, token, prompt, session):
                owners.add(threading.get_ident())
                self.count += 1
                if self.count == 30:
                    self.ready.set()
                await asyncio.wait_for(self.ready.wait(), 5)
                return session, prompt
            async def prune(self):
                owners.add(threading.get_ident())
            async def close(self):
                owners.add(threading.get_ident())
        worker = AsyncBrowserWorker(Engine, adapter)
        try:
            with ThreadPoolExecutor(max_workers=30) as pool:
                futures = [pool.submit(worker.run, '300', 'fixture', str(i), str(i)) for i in range(30)]
                self.assertEqual([future.result(10) for future in futures], [(str(i), str(i)) for i in range(30)])
        finally:
            worker.close()
        self.assertEqual(owners, {worker.thread.ident})
        self.assertFalse(worker.thread.is_alive())
        with self.assertRaises(adapter.AdapterError):
            worker.run('300', 'fixture', 'after stop', None)

    def test_factory_failure_rejects_without_hanging(self):
        def fail():
            raise ValueError('fixture')
        worker = AsyncBrowserWorker(fail, adapter)
        try:
            with self.assertRaises(adapter.AdapterError) as raised:
                worker.run('300', 'fixture', 'unused', None)
            self.assertEqual(raised.exception.status, 503)
        finally:
            worker.close()


class EngineTests(unittest.IsolatedAsyncioTestCase):
    async def test_concurrent_gates_reject_a_valid_model_from_another_request(self):
        engine = SimpleNamespace(api=adapter)
        starts = [multiplex_browser.BrowserStart(engine, None, None, None, model, 'high') for model in adapter.MODELS]
        async def verify(start):
            start.armed, start.project = True, 'project'
            route = mock.Mock(abort=mock.AsyncMock(), continue_=mock.AsyncMock())
            other = next(model for model in adapter.MODELS if model != start.model)
            route.request = SimpleNamespace(url=adapter.BASE+adapter.START, method='POST',
                post_data_json={'metadata':{'model':other,'reasoning_effort':'high','projectId':'project'}})
            await start.route(route)
            self.assertFalse(start.sent)
            route.continue_.assert_not_awaited()
            route.abort.assert_awaited_once()
        await asyncio.gather(*(verify(start) for start in starts))

    async def test_project_id_must_be_confirmed_by_the_official_api(self):
        actor = multiplex_browser.AccountBrowser(SimpleNamespace(api=adapter), '300', 'fixture')
        actor.page = SimpleNamespace(evaluate=mock.AsyncMock(return_value={'status':200,'data':{'uuid':'wrong'}}))
        with self.assertRaises(adapter.AdapterError) as raised:
            await actor.create_project()
        self.assertEqual(raised.exception.code, 'project_creation_failed')
        async def confirmed(_script, args):
            self.assertEqual(args['path'], '/api/projects')
            self.assertEqual(set(args['body']), {'project_uuid','title'})
            return {'status':200,'data':{'uuid':args['body']['project_uuid']}}
        actor.page.evaluate = confirmed
        project = await actor.create_project()
        self.assertIsNotNone(adapter.PROJECT_ID.fullmatch(project))

    async def test_poll_carrier_requires_the_official_fetch_wrapper_before_start(self):
        engine = SimpleNamespace(api=adapter)
        actor = multiplex_browser.AccountBrowser(engine, '300', 'fixture')
        page = mock.Mock()
        page.url = adapter.BASE + '/'
        page.add_init_script = mock.AsyncMock()
        page.wait_for_function = mock.AsyncMock(side_effect=TimeoutError('fixture'))
        page.goto = mock.AsyncMock(return_value=SimpleNamespace(status=200))
        context = mock.Mock(add_cookies=mock.AsyncMock(),new_page=mock.AsyncMock(return_value=page),close=mock.AsyncMock())
        browser = SimpleNamespace(new_context=mock.AsyncMock(return_value=context))
        with mock.patch.object(multiplex_browser.BrowserGate,'install',new=mock.AsyncMock()):
            with self.assertRaises(adapter.AdapterError) as raised:
                await actor.open(browser, 'fixture')
        self.assertEqual(raised.exception.code, 'poll_carrier_unavailable')
        context.close.assert_awaited_once()
        self.assertIsNone(actor.context)

    async def test_memory_pressure_only_retires_idle_accounts(self):
        with tempfile.TemporaryDirectory() as directory:
            engine = multiplex_browser.MultiplexBrowser(adapter.State(directory), 'fixture', adapter)
            idle = SimpleNamespace(refs=0,used=float('inf'),created=float('inf'),close=mock.AsyncMock())
            active = SimpleNamespace(refs=1,used=0,created=0,close=mock.AsyncMock())
            engine.actors = {'idle':idle,'active':active}
            with mock.patch.object(multiplex_browser, 'cgroup_memory_bytes', lambda:800*1024*1024):
                await engine.prune()
            idle.close.assert_awaited_once()
            active.close.assert_not_awaited()
            self.assertEqual(engine.actors, {'active':active})

    async def test_browser_gate_preserves_model_project_and_single_start_budget(self):
        engine = SimpleNamespace(api=adapter)
        for change in ({'model':'gpt-6-astra'}, {'reasoning_effort':'low'}, {'projectId':'another'}):
            start = multiplex_browser.BrowserStart(engine, None, None, None)
            start.armed, start.project = True, 'project'
            route = mock.Mock()
            route.request = SimpleNamespace(url=adapter.BASE + adapter.START, method='POST',
                post_data_json={'metadata':dict(model=adapter.MODEL, reasoning_effort='medium', projectId='project') | change})
            route.abort, route.continue_ = mock.AsyncMock(), mock.AsyncMock()
            await start.route(route)
            route.abort.assert_awaited_once()
            route.continue_.assert_not_awaited()
            self.assertFalse(start.sent)
        start = multiplex_browser.BrowserStart(engine, None, None, None)
        start.armed, start.project = True, 'project'
        route.request.post_data_json = {'metadata':{'model':adapter.MODEL,'reasoning_effort':'medium','projectId':'project'}}
        route.abort.reset_mock()
        await start.route(route)
        await start.route(route)
        route.continue_.assert_awaited_once()
        route.abort.assert_awaited_once()
        self.assertTrue(start.violation)

    async def test_poll_failure_retains_record_and_releases_execution_slot(self):
        with tempfile.TemporaryDirectory() as directory:
            state = adapter.State(directory)
            engine = multiplex_browser.MultiplexBrowser(state, 'fixture', adapter)
            actor = SimpleNamespace(refs=0, projects={}, poll=mock.AsyncMock(side_effect=adapter.AdapterError(502,'poll_failed','fixture')))
            async def account(*_args):
                actor.refs += 1
                return actor
            engine.account = account
            class Start:
                def __init__(self, engine, actor, journal, session, model, effort):
                    self.sent = self.violation = self.cache_hit = False
                    self.request_id = 'fixture-request'
                    self.project = 'fixture-project'
                    self.journal = journal
                async def run(self, _prompt):
                    self.sent = True
                    self.journal.update(stage='polling', request_id=self.request_id, turn_state='opaque')
                    return {'status':'started'}, {'request_id':self.request_id, 'turn_state':'opaque'}
                async def close(self):
                    pass
            with mock.patch.object(multiplex_browser,'BrowserStart',Start), \
                    mock.patch.object(multiplex_browser,'cgroup_memory_bytes',lambda:None):
                with self.assertRaises(adapter.AdapterError):
                    await engine.run('300','fixture','prompt','session')
            records = list((state.pending/'300').iterdir())
            self.assertEqual(len(records), 1)
            self.assertEqual(json.loads(records[0].read_text())['turn_state'], 'opaque')
            self.assertEqual((actor.refs, engine.admission.running, engine.admission.outstanding), (0,0,0))
            actor.poll.assert_awaited_once()
            with self.assertRaises(adapter.AdapterError) as blocked:
                await engine.run('300','fixture','retry','session')
            self.assertEqual(blocked.exception.status, 409)

    async def test_thirty_starts_have_independent_poll_bodies_and_journals(self):
        with tempfile.TemporaryDirectory() as directory:
            state = adapter.State(directory)
            engine = multiplex_browser.MultiplexBrowser(state, 'fixture', adapter, active=30, per_account=30)
            ready = asyncio.Event()
            seen = {}
            class Actor:
                refs = 0
                projects = OrderedDict()
                async def poll(self, body):
                    rid = body['request_id']
                    self.assert_state(body, rid)
                    seen[rid] = body
                    if len(seen) == 30:
                        ready.set()
                    await asyncio.wait_for(ready.wait(), 3)
                    return {'request_id':rid, 'status':'completed', 'response':{
                        'status':'success','payload':{'output':[{'type':'message','content':[{'text':rid}]}]}}}
                def assert_state(self, body, rid):
                    if body['turn_state'] != {'owner':rid}:
                        raise AssertionError('turn state crossed jobs')
            actor = Actor()
            async def account(*_args):
                actor.refs += 1
                return actor
            engine.account = account
            class Start:
                def __init__(self, engine, actor, journal, session, model, effort):
                    self.journal = journal
                    self.request_id = journal.local_id
                    self.project = 'fixture-project'
                    self.sent = self.violation = self.cache_hit = False
                async def run(self, prompt):
                    self.sent = True
                    rid = self.request_id
                    return {'request_id':rid,'status':'running','turn_state':{'owner':rid}}, {'request_id':rid}
                async def close(self):
                    pass
            with mock.patch.object(multiplex_browser, 'BrowserStart', Start), \
                    mock.patch.object(multiplex_browser, 'cgroup_memory_bytes', lambda:None):
                results = await asyncio.gather(*(engine.run('300','fixture',f'prompt-{i}',str(i)) for i in range(30)))
            self.assertEqual(len({rid for rid, _ in results}), 30)
            self.assertTrue(all(rid == answer for rid, answer in results))
            self.assertEqual(len(list(state.receipts.iterdir())), 30)
            self.assertEqual(list(state.pending.iterdir()), [])
            self.assertEqual(actor.refs, 0)

    async def test_unknown_turn_only_blocks_its_own_conversation(self):
        with tempfile.TemporaryDirectory() as directory:
            state = adapter.State(directory)
            blocked = TurnJournal(state, adapter, '300', 'unresolved')
            blocked.begin()
            blocked.update(stage='polling', request_id='unknown')
            engine = multiplex_browser.MultiplexBrowser(state, 'fixture', adapter)
            engine.account = mock.AsyncMock(side_effect=RuntimeError('stop before browser launch'))
            with self.assertRaises(adapter.AdapterError) as existing:
                await engine.run('300','fixture','retry','unresolved')
            self.assertEqual(existing.exception.status, 409)
            engine.account.assert_not_awaited()
            with self.assertRaises(RuntimeError):
                await engine.run('300','fixture','other','independent')
            engine.account.assert_awaited_once()
            self.assertEqual(list(blocked.directory.iterdir()), [blocked.path])
            self.assertEqual(json.loads(blocked.path.read_text())['request_id'], 'unknown')

class MemoryAdmissionTests(unittest.IsolatedAsyncioTestCase):
    async def test_transient_pressure_collects_and_waits_without_submitting(self):
        with tempfile.TemporaryDirectory() as directory:
            engine = multiplex_browser.MultiplexBrowser(adapter.State(directory), 'fixture', adapter)
            engine.memory_wait_seconds = 1
            actor = SimpleNamespace(gate=SimpleNamespace(session=SimpleNamespace(send=mock.AsyncMock())))
            journal = SimpleNamespace(local_id='fixture-local')
            samples = iter([800, 800, 600])
            with mock.patch.object(multiplex_browser, 'cgroup_memory_bytes', lambda:next(samples, 600)*1024*1024), \
                    mock.patch.object(engine, 'observe') as observe:
                await engine.wait_for_memory(actor, journal)
            actor.gate.session.send.assert_awaited_once_with('HeapProfiler.collectGarbage')
            self.assertEqual([c.args[0] for c in observe.call_args_list], ['prism_memory_wait','prism_memory_ready'])

    async def test_sustained_pressure_rejects_and_cancellation_remains_cancellable(self):
        with tempfile.TemporaryDirectory() as directory:
            engine = multiplex_browser.MultiplexBrowser(adapter.State(directory), 'fixture', adapter)
            engine.memory_wait_seconds = 0.02
            actor = SimpleNamespace(gate=SimpleNamespace(session=SimpleNamespace(send=mock.AsyncMock())))
            with mock.patch.object(multiplex_browser, 'cgroup_memory_bytes', lambda:800*1024*1024), \
                    mock.patch.object(engine, 'observe'):
                with self.assertRaises(adapter.AdapterError) as error:
                    await engine.wait_for_memory(actor, SimpleNamespace(local_id='fixture'))
                self.assertEqual(error.exception.code, 'resource_pressure')
                engine.memory_wait_seconds = 30
                task = asyncio.create_task(engine.wait_for_memory(actor, None))
                await asyncio.sleep(0)
                task.cancel()
                with self.assertRaises(asyncio.CancelledError):
                    await task

    async def test_closed_editor_releases_listener_before_closing_page(self):
        start = multiplex_browser.BrowserStart(SimpleNamespace(api=adapter), None, None, None)
        page = mock.Mock(close=mock.AsyncMock())
        start.page = page
        await start.close()
        await start.close()
        page.remove_listener.assert_called_once_with('response', start.response)
        page.close.assert_awaited_once_with(run_before_unload=False)
        self.assertIsNone(start.page)

    async def test_lifecycle_telemetry_does_not_include_request_material(self):
        with tempfile.TemporaryDirectory() as directory:
            engine = multiplex_browser.MultiplexBrowser(adapter.State(directory), 'fixture', adapter)
            journal = SimpleNamespace(local_id='local-fixture', token='secret-token', turn_state='secret-state')
            with self.assertLogs('prism.lifecycle') as captured:
                engine.observe('prism_prepare_start', journal, model='gpt-6.1-sol', effort='xhigh')
            raw=' '.join(captured.output)
            self.assertNotIn('secret-token',raw)
            self.assertNotIn('secret-state',raw)
            self.assertIn('memory_bytes',raw)
            self.assertIn('xhigh',raw)


class ProjectRuntimeTests(unittest.IsolatedAsyncioTestCase):
    async def test_reported_runtime_throttle_is_recognized_before_editor_submission(self):
        editor=SimpleNamespace(is_visible=mock.AsyncMock(return_value=True))
        blocked=SimpleNamespace(is_visible=mock.AsyncMock(return_value=True))
        page=SimpleNamespace(locator=mock.Mock(return_value=editor),get_by_text=mock.Mock(return_value=SimpleNamespace(first=blocked)))
        with self.assertRaises(adapter.AdapterError) as error:
            await multiplex_browser.wait_for_editor(page,adapter)
        self.assertEqual(error.exception.code,'project_runtime_rate_limited')
        self.assertTrue(multiplex_browser.RUNTIME_RATE_LIMIT.search('项目运行环境的启动请求受到限流。请等待后重新连接'))
        editor.is_visible.assert_not_awaited()

    async def test_project_creation_429_is_not_retried_and_cools_following_requests(self):
        with tempfile.TemporaryDirectory() as directory:
            engine=multiplex_browser.MultiplexBrowser(adapter.State(directory),'fixture',adapter)
            engine.account=mock.AsyncMock(side_effect=adapter.AdapterError(429,'project_runtime_rate_limited','fixture'))
            with self.assertRaises(adapter.AdapterError):
                await engine.run('300','fixture','prompt','one')
            expiry=engine.runtime_cooldowns['300']
            with self.assertRaises(adapter.AdapterError) as error:
                await engine.run('300','fixture','prompt','two')
            self.assertEqual(error.exception.code,'project_runtime_rate_limited')
            engine.account.assert_awaited_once()
            self.assertEqual(engine.runtime_cooldowns['300'],expiry)
            self.assertEqual(list(engine.state.pending.iterdir()),[])


class PollDiagnosticsTests(unittest.IsolatedAsyncioTestCase):
    async def test_failed_poll_reports_status_without_body_or_turn_state(self):
        observer=mock.Mock()
        engine=SimpleNamespace(api=adapter,observe=observer)
        actor=multiplex_browser.AccountBrowser(engine,'300','fixture-secret')
        actor.page=SimpleNamespace(evaluate=mock.AsyncMock(return_value={'status':429}))
        with self.assertRaises(adapter.AdapterError) as error:
            await actor.poll({'request_id':'request-secret','turn_state':'state-secret'})
        self.assertEqual(error.exception.code,'poll_failed')
        observer.assert_called_once_with('prism_poll_failed',sent=False,http_status=429,transport_error=None)
        self.assertEqual(actor.expected,{})
        self.assertNotIn('secret',str(observer.call_args))


class TerminalConcurrencyTests(unittest.IsolatedAsyncioTestCase):
    async def test_immediate_upstream_failure_does_not_count_as_live_polling(self):
        with tempfile.TemporaryDirectory() as directory:
            engine=multiplex_browser.MultiplexBrowser(adapter.State(directory),'fixture',adapter)
            actor=SimpleNamespace(refs=0,projects={})
            async def account(*_args):
                actor.refs+=1
                return actor
            engine.account=account
            class Start:
                def __init__(self,engine,actor,journal,session,model,effort):
                    self.sent=self.violation=self.cache_hit=False
                    self.request_id='terminal-fixture'
                    self.project='project'
                    self.phase='awaiting_start'
                async def run(self,_prompt):
                    self.sent=True
                    return {'request_id':self.request_id,'status':'failed'},None
                async def close(self):pass
            with mock.patch.object(multiplex_browser,'BrowserStart',Start), \
                    mock.patch.object(multiplex_browser,'cgroup_memory_bytes',lambda:None), \
                    mock.patch.object(engine,'observe') as observe:
                with self.assertRaises(adapter.AdapterError):
                    await engine.run('300','fixture','prompt','session','gpt-6.1-sol','xhigh')
            events=[c.args[0] for c in observe.call_args_list]
            self.assertNotIn('prism_poll_start',events)
            self.assertIn('prism_upstream_terminal_failure',events)
            self.assertEqual((engine.polling,engine.preparing,actor.refs),(0,0,0))
            self.assertEqual(list(engine.state.pending.iterdir()),[])

class RuntimeReconnectTests(unittest.IsolatedAsyncioTestCase):
    def request(self, start, *, prompt='original', token='sandbox-a'):
        body={'input':[{'role':'user','content':prompt}], 'conversationId':'conversation-a',
              'metadata':{'model':start.model,'reasoning_effort':start.effort,'projectId':'project', 'sandbox_token':token}}
        route=mock.Mock(abort=mock.AsyncMock(),continue_=mock.AsyncMock())
        route.request=SimpleNamespace(url=adapter.BASE+adapter.START,method='POST',post_data_json=body)
        return route

    async def response(self, start, route, reason):
        data={'request_id':'attempt-'+str(start.start_attempts),'status':'completed',
              'response':{'status':'error','payload':{'reason':reason}}}
        response=SimpleNamespace(url=adapter.BASE+adapter.START,status=200,request=route.request,json=mock.AsyncMock(return_value=data))
        await start.response(response)
        return data

    async def test_only_explicit_reconnect_allows_ui_to_continue_same_intent(self):
        engine=SimpleNamespace(api=adapter,observe=mock.Mock())
        journal=SimpleNamespace(local_id='fixture',update=mock.Mock())
        start=multiplex_browser.BrowserStart(engine,None,journal,'scope','gpt-6.1-sol','xhigh')
        start.project,start.armed='project',True
        first=self.request(start)
        await start.route(first)
        await self.response(start,first,'sandbox_reconnecting')
        self.assertFalse(start.sent)
        self.assertFalse(start.start_response.done())
        retry=self.request(start,token='sandbox-b')
        await start.route(retry)
        retry.continue_.assert_awaited_once()
        await self.response(start,retry,'sandbox_reconnecting')
        last=self.request(start,token='sandbox-c')
        await start.route(last)
        await self.response(start,last,'sandbox_reconnecting')
        self.assertTrue(start.start_response.done())
        self.assertEqual(start.start_attempts,3)
        fourth=self.request(start,token='sandbox-d')
        await start.route(fourth)
        fourth.continue_.assert_not_awaited()

    async def test_reconnect_never_authorizes_changed_prompt_or_unknown_failure(self):
        for reason in ['sandbox_reconnecting','unknown']:
            engine=SimpleNamespace(api=adapter,observe=mock.Mock())
            start=multiplex_browser.BrowserStart(engine,None,SimpleNamespace(update=mock.Mock()),None)
            start.project,start.armed='project',True
            first=self.request(start)
            await start.route(first)
            await self.response(start,first,reason)
            second=self.request(start,prompt='different' if reason=='sandbox_reconnecting' else 'original')
            await start.route(second)
            second.continue_.assert_not_awaited()
            second.abort.assert_awaited_once()

    async def test_failure_reason_never_returns_arbitrary_upstream_text(self):
        for value in ['private-token','https://private.invalid','Bearer secret',{'token':'secret'},None]:
            self.assertEqual(adapter.terminal_failure_reason({'response':{'payload':{'reason':value}}}),'unknown')


class ErrorDiagnosticsTests(unittest.TestCase):
    def test_only_documented_diagnostic_enums_and_numeric_status_are_public(self):
        value={'response':{'payload':{'message':'secret-prompt','rootCause':'secret-token','diagnostics':{
            'operation':'start','code':'workspace_sync_timeout','httpStatus':504,'requestId':'secret-id','token':'secret'}}}}
        self.assertEqual(adapter.terminal_failure_diagnostics(value),{
            'upstream_operation':'start','upstream_code':'workspace_sync_timeout','upstream_status':504})
        value['response']['payload']['diagnostics']={'operation':'secret','code':'secret','httpStatus':'secret'}
        self.assertEqual(adapter.terminal_failure_diagnostics(value),{})
