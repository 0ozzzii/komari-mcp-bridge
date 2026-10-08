import hashlib
import http.server
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


class DownloadTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.path = Path(self.temp.name)
        self.content = b'new release binary\x00\xff\n'
        self.asset = 'komari-agent-linux-amd64'
        self.responses = {self.asset: self.content,
                          'sha256sums.txt': (hashlib.sha256(self.content).hexdigest() + '  ' + self.asset + '\n').encode()}
        responses = self.responses
        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                body = responses.get(self.path.rsplit('/', 1)[-1])
                self.send_response(200 if body is not None else 404)
                self.end_headers()
                if body is not None:
                    self.wfile.write(body)
            def log_message(self, *args):
                pass
        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.url = 'http://127.0.0.1:' + str(self.server.server_port) + '/v1.0.0/' + self.asset
        self.dest = self.path / 'installed'
        self.dest.write_bytes(b'existing binary')

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        self.temp.cleanup()

    def fetch(self):
        return subprocess.run(['bash', '-c', 'source "$1"; komari_fetch_verified "$2" "$3"',
                               'test', str(ROOT / 'release/download.sh'), self.url, str(self.dest)],
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    def test_verified_candidate_replaces_old_binary(self):
        self.assertEqual(self.fetch().returncode, 0)
        self.assertEqual(self.dest.read_bytes(), self.content)
        self.assertEqual(self.dest.stat().st_mode & 0o777, 0o755)
        self.assertFalse(list(self.path.glob('*.download.*')))

    def test_wrong_missing_and_duplicate_checksum_preserve_binary(self):
        for manifest in (b'0' * 64 + b'  ' + self.asset.encode() + b'\n', b'other file\n',
                         self.responses['sha256sums.txt'] * 2, None):
            with self.subTest(manifest=manifest):
                self.responses['sha256sums.txt'] = manifest
                self.assertNotEqual(self.fetch().returncode, 0)
                self.assertEqual(self.dest.read_bytes(), b'existing binary')
                self.assertFalse(list(self.path.glob('*.download.*')))

    def test_panel_upgrade_failure_does_not_stop_service(self):
        self.responses['sha256sums.txt'] = b'invalid checksum'
        script = r'''
source "$1"
INSTALL_DIR=$2; BINARY_PATH="$2/installed"; SERVICE_NAME=test
progress_reset() { :; }; log_step() { :; }; ui_msgbox() { :; }
is_installed() { return 0; }; check_systemd() { return 0; }
select_edition() { :; }; select_channel() { :; }; detect_arch() { echo amd64; }
get_download_url() { echo "$TEST_DOWNLOAD_URL"; }
systemctl() { echo "$*" >> "$INSTALL_DIR/service-operations"; }
upgrade_komari
'''
        env = dict(os.environ, TEST_DOWNLOAD_URL=self.url)
        result = subprocess.run(['bash', '-c', script, 'test', str(ROOT / 'install-komari.sh'), str(self.path)],
                                env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.path / 'service-operations').exists())
        self.assertEqual(self.dest.read_bytes(), b'existing binary')

    def test_embedded_helpers_match_canonical_source(self):
        for name in ('install.sh', 'install-komari.sh'):
            source = (ROOT / name).read_text().split('# BEGIN KOMARI RELEASE DOWNLOAD\n')[1].split('# END KOMARI RELEASE DOWNLOAD\n')[0]
            self.assertEqual(source, (ROOT / 'release/download.sh').read_text())


class ManifestTests(unittest.TestCase):
    def test_complete_matrix_is_required_and_manifest_hashes_real_bytes(self):
        manifest = module('release_manifest', ROOT / 'release/manifest.py')
        config = json.loads((ROOT / 'release/config.json').read_text())
        with tempfile.TemporaryDirectory() as work:
            path = Path(work)
            with self.assertRaisesRegex(ValueError, 'Missing'):
                manifest.generate(path, 'v1.0.0')
            for name in manifest.binary_names(config):
                (path / name).write_bytes(('fixture:' + name).encode())
            manifest.generate(path, 'v1.0.0')
            result = subprocess.run(['sha256sum', '-c', 'sha256sums.txt'], cwd=work, stdout=subprocess.PIPE)
            self.assertEqual(result.returncode, 0)
            (path / manifest.binary_names(config)[0]).write_bytes(b'corrupted')
            result = subprocess.run(['sha256sum', '-c', 'sha256sums.txt'], cwd=work,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            self.assertNotEqual(result.returncode, 0)


class GitFixture(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.work = Path(self.temp.name)

    def tearDown(self):
        self.temp.cleanup()

    def git(self, repo, *args):
        return subprocess.check_output(['git', '-C', str(repo), *args], stderr=subprocess.PIPE).decode().strip()

    def init(self, path):
        path.mkdir()
        self.git(path, 'init', '-q')
        self.git(path, 'config', 'user.email', 'tests@example.invalid')
        self.git(path, 'config', 'user.name', 'Release tests')

    def commit(self, path):
        self.git(path, 'add', '-A')
        self.git(path, 'commit', '-qm', 'fixture')
        return self.git(path, 'rev-parse', 'HEAD')


class PrepareTests(GitFixture):
    def test_repeatable_preparation_and_user_edits_outside_hunks_are_protected(self):
        repo = self.work / 'upstream'
        self.init(repo)
        content = 'start\n' + ''.join('line %s\n' % n for n in range(20))
        (repo / 'file.txt').write_text(content)
        base = self.commit(repo)
        expected = content.replace('start', 'our optimization')
        (repo / 'file.txt').write_text(expected)
        patch = subprocess.check_output(['git', '-C', str(repo), 'diff'])
        (repo / 'file.txt').write_text(content)
        config_root = self.work / 'root'
        (config_root / 'release').mkdir(parents=True)
        (config_root / 'change.patch').write_bytes(patch)
        (config_root / 'release/config.json').write_text(json.dumps({'upstream': {'agent': {
            'commit': base, 'patches': ['change.patch'], 'overlay': None}}}))
        helper = module('prepare_test', ROOT / 'release/prepare.py')
        helper.ROOT = config_root
        helper.prepare('agent', repo)
        helper.prepare('agent', repo)
        self.assertEqual((repo / 'file.txt').read_text(), expected)
        (repo / 'file.txt').write_text(expected.replace('line 19', 'user-owned edit'))
        with self.assertRaisesRegex(RuntimeError, 'Modified patch target'):
            helper.prepare('agent', repo)
        self.assertIn('user-owned edit', (repo / 'file.txt').read_text())


class SyncTests(GitFixture):
    def fixture(self, conflict=False):
        upstream = self.work / 'official'
        ours = self.work / 'ours'
        self.init(upstream)
        content = ''.join('line %s\n' % n for n in range(30))
        (upstream / 'file.txt').write_text(content)
        base = self.commit(upstream)
        (upstream / 'file.txt').write_text(content.replace('line 0' if not conflict else 'line 29', 'official addition'))
        new = self.commit(upstream)
        self.init(ours)
        (ours / 'file.txt').write_text(content.replace('line 29', 'our MCP addition'))
        (ours / 'release').mkdir()
        c = {'upstream': {k: {'repository': 'test/' + k, 'commit': base, 'patches': [], 'overlay': None}
                          for k in ('panel', 'agent', 'web')}}
        (ours / 'release/config.json').write_text(json.dumps(c))
        # Store the original blob as well, like the actual imported snapshot.
        (ours / 'file.txt').write_text(content)
        self.commit(ours)
        (ours / 'file.txt').write_text(content.replace('line 29', 'our MCP addition'))
        self.commit(ours)
        return upstream, ours, base, new

    def test_three_way_merge_preserves_our_changes_without_merging_branch(self):
        upstream, ours, base, new = self.fixture()
        sync = module('sync_test', ROOT / 'release/sync-upstream.py')
        previous = self.git(ours, 'rev-parse', 'HEAD')
        result = sync.sync(ours, {'panel': new, 'agent': base, 'web': base}, {'panel': str(upstream)})
        self.assertEqual(result, 'ready')
        self.assertIn('official addition', (ours / 'file.txt').read_text())
        self.assertIn('our MCP addition', (ours / 'file.txt').read_text())
        self.assertEqual(self.git(ours, 'rev-parse', 'HEAD'), previous)

    def test_conflict_keeps_code_and_version_pins_untouched(self):
        upstream, ours, base, new = self.fixture(conflict=True)
        sync = module('sync_conflict', ROOT / 'release/sync-upstream.py')
        original = (ours / 'file.txt').read_bytes()
        pins = (ours / 'release/config.json').read_bytes()
        result = sync.sync(ours, {'panel': new, 'agent': base, 'web': base}, {'panel': str(upstream)})
        self.assertEqual(result, 'conflict')
        self.assertEqual((ours / 'file.txt').read_bytes(), original)
        self.assertEqual((ours / 'release/config.json').read_bytes(), pins)
        self.assertIn('不能合并', (ours / 'release/upstream-report.md').read_text())

    def test_agent_rebase_preserves_patch_and_installer_identity(self):
        import shutil
        upstream = self.work / 'agent'
        ours = self.work / 'project'
        self.init(upstream)
        source = ''.join('line %s\n' % n for n in range(30))
        (upstream / 'agent.go').write_text(source)
        (upstream / 'install.sh').write_text('#!/bin/sh\necho official\n')
        (upstream / 'install.ps1').write_text('Write-Host official\n')
        (upstream / 'LICENSE').write_text('upstream license baseline\n')
        base = self.commit(upstream)
        (upstream / 'agent.go').write_text(source.replace('line 29', 'our scope optimization'))
        patch = subprocess.check_output(['git','-C',str(upstream),'diff'])
        (upstream / 'agent.go').write_text(source.replace('line 0', 'upstream fix'))
        (upstream / 'LICENSE').write_text('upstream license candidate\n')
        new = self.commit(upstream)
        self.init(ours)
        (ours / 'release').mkdir(); (ours / 'agent-patches').mkdir()
        shutil.copyfile(ROOT / 'release/prepare.py', ours / 'release/prepare.py')
        (ours / 'agent-patches/change.patch').write_bytes(patch)
        (ours / 'install.sh').write_text('#!/bin/sh\necho own_release\n')
        (ours / 'install.ps1').write_text('Write-Host own_release\n')
        (ours / 'LICENSE.komari-agent').write_text('upstream license baseline\n')
        config = {'upstream': {k: {'repository':'test/'+k,'commit':base,'patches':[],'overlay':None} for k in ('panel','agent','web')}}
        config['upstream']['agent']['patches']=['agent-patches/change.patch']
        (ours / 'release/config.json').write_text(json.dumps(config))
        self.commit(ours)
        sync=module('agent_sync_test',ROOT / 'release/sync-upstream.py')
        self.assertEqual(sync.sync(ours,{'panel':base,'agent':new,'web':base},{'agent':str(upstream)}),'ready')
        updated=json.loads((ours / 'release/config.json').read_text())
        self.assertEqual(updated['upstream']['agent']['commit'],new)
        merged=(ours / 'agent-patches/upstream-compatible.patch').read_text()
        self.assertIn('our scope optimization',merged)
        self.assertIn('own_release',merged)
        self.assertIn('own_release',(ours / 'install.sh').read_text())
        self.assertEqual((ours / 'LICENSE.komari-agent').read_text(), 'upstream license candidate\n')
        checkout=self.work / 'prepared'
        subprocess.run(['git','clone','-q',str(upstream),str(checkout)],check=True)
        subprocess.run(['python3',str(ours/'release/prepare.py'),'agent',str(checkout)],check=True)
        self.assertIn('upstream fix',(checkout/'agent.go').read_text())
        self.assertIn('our scope optimization',(checkout/'agent.go').read_text())

    def test_dirty_checkout_is_never_overwritten(self):
        upstream, ours, base, new = self.fixture()
        (ours / 'user-work.txt').write_text('uncommitted')
        sync = module('sync_dirty', ROOT / 'release/sync-upstream.py')
        with self.assertRaisesRegex(RuntimeError, 'clean'):
            sync.sync(ours, {'panel': new, 'agent': base, 'web': base}, {'panel': str(upstream)})
        self.assertEqual((ours / 'user-work.txt').read_text(), 'uncommitted')



class ReviewGateTests(unittest.TestCase):
    def setUp(self):
        self.gate = module('review_gate_tests', ROOT / 'release/review-gate.py')
        self.sha = '1' * 40
        self.pr = {'state': 'open', 'draft': False, 'mergeable': True,
                   'user': {'login': 'github-actions[bot]'},
                   'head': {'ref': 'upstream/official-sync-test', 'sha': self.sha, 'repo': {'full_name': 'owner/repo'}},
                   'base': {'repo': {'full_name': 'owner/repo'}}}
        self.review = {'id': 1, 'user': {'login': 'review-app[bot]'}, 'state': 'APPROVED', 'commit_id': self.sha}
        self.run = {'workflow_id': 123, 'head_sha': self.sha, 'run_number': 1,
                    'status': 'completed', 'conclusion': 'success'}

    def test_only_current_authorized_review_and_complete_build_allow_merge(self):
        self.assertTrue(self.gate.eligible(self.pr, [self.review], [self.run], {'review-app[bot]'}, 123)[0])
        for change in ({'commit_id': '2' * 40}, {'state': 'CHANGES_REQUESTED'}, {'user': {'login': 'unapproved-account'}}):
            with self.subTest(change=change):
                review = dict(self.review, **change)
                self.assertFalse(self.gate.eligible(self.pr, [review], [self.run], {'review-app[bot]'}, 123)[0])
        self.assertFalse(self.gate.eligible(self.pr, [self.review], [self.run], set(), 123)[0])

    def test_new_failure_cannot_be_hidden_by_old_pass(self):
        bad = dict(self.run, run_number=2, conclusion='failure')
        self.assertFalse(self.gate.eligible(self.pr, [self.review], [self.run, bad], {'review-app[bot]'}, 123)[0])
        bad = dict(self.run, run_attempt=2, conclusion='failure')
        self.assertFalse(self.gate.eligible(self.pr, [self.review], [self.run, bad], {'review-app[bot]'}, 123)[0])
        for status in (dict(draft=True), dict(mergeable=None), dict(mergeable=False)):
            self.assertFalse(self.gate.eligible(dict(self.pr, **status), [self.review], [self.run], {'review-app[bot]'}, 123)[0])


class ModelReviewTests(unittest.TestCase):
    def setUp(self):
        self.ai = module('ai_review_tests', ROOT / 'release/ai-review.py')
        self.sha = '1' * 40
        self.clock_time = 0.0
        self.providers = [('primary', 'https://primary.example/v1', 'test-key', 'model-a'),
                          ('backup', 'https://backup.example/v1', 'test-key-b', 'model-b')]

    def invoke(self, providers=None, call=None):
        def sleep(seconds):
            self.clock_time += seconds
        kwargs = {'clock': lambda: self.clock_time, 'sleep': sleep, 'jitter': lambda: 0}
        if call is not None:
            kwargs['call'] = call
        return self.ai.review_with_fallback({}, self.sha, self.providers if providers is None else providers, **kwargs)

    def result(self, decision):
        return {'decision': decision, 'reviewed_sha': self.sha, 'summary': 'test review', 'findings': []}

    def test_primary_rejection_and_uncertainty_never_trigger_backup(self):
        for decision in ('request_changes', 'uncertain', 'approve'):
            calls = []
            def call(url, *args):
                calls.append(url)
                return self.result(decision)
            result = self.invoke(call=call)
            self.assertEqual(result['decision'], decision)
            self.assertEqual(len(calls), 1)
            self.assertEqual(result['provider'], 'primary')

    def test_only_technical_failure_fails_over_and_total_failure_never_approves(self):
        calls = []
        def call(url, *args):
            calls.append(url)
            if 'primary' in url:
                raise self.ai.ProviderFailure('simulated outage')
            return self.result('approve')
        result = self.invoke(call=call)
        self.assertEqual(result['provider'], 'backup')
        self.assertEqual(len(calls), 2)
        def fail(*args):
            raise self.ai.ProviderFailure('simulated outage')
        self.assertEqual(self.invoke(call=fail)['decision'], 'uncertain')
        self.assertEqual(self.invoke(providers=[])['decision'], 'uncertain')

    def test_invalid_or_wrong_sha_responses_rejected_and_contradictory_approval_vetoed(self):
        for value in ('not json', '[]', json.dumps(dict(self.result('approve'), reviewed_sha='2' * 40))):
            with self.assertRaises(self.ai.ProviderFailure):
                self.ai.parse_review(value, self.sha)
        value = self.result('approve')
        value['findings'] = [{'severity': 'high', 'path': 'auth.go', 'reason': 'authorization regression'}]
        self.assertEqual(self.ai.parse_review(json.dumps(value), self.sha)['decision'], 'request_changes')

    def test_429_retries_honor_server_and_serial_rpm_limit(self):
        times = []
        def call(*args):
            times.append(self.clock_time)
            if len(times) == 1:
                raise self.ai.ProviderFailure('HTTP 429', retryable=True, retry_after=35)
            if len(times) == 2:
                raise self.ai.ProviderFailure('HTTP 503', retryable=True)
            return self.result('approve')
        self.assertEqual(self.invoke(call=call)['decision'], 'approve')
        self.assertEqual(times, [0, 35, 55])

    def test_excessive_retry_after_switches_without_retrying_too_early(self):
        urls = []
        def call(url,*args):
            urls.append(url)
            if 'primary' in url:
                raise self.ai.ProviderFailure('HTTP 429', retryable=True, retry_after=3600)
            return self.result('approve')
        self.assertEqual(self.invoke(call=call)['provider'], 'backup')
        self.assertEqual(len(urls), 2)
        self.assertEqual(self.clock_time, 10)
        self.assertEqual(self.ai.retry_after_seconds('12'), 12)
        self.assertIsNone(self.ai.retry_after_seconds('invalid'))

    def test_retry_count_is_bounded_and_all_outages_keep_pr_unmerged(self):
        times = []
        def call(*args):
            times.append(self.clock_time)
            raise self.ai.ProviderFailure('HTTP 429', retryable=True)
        self.assertEqual(self.invoke(call=call)['decision'], 'uncertain')
        self.assertEqual(len(times), 6)
        self.assertTrue(all(b-a >= 10 for a,b in zip(times,times[1:])))


class NotificationTests(unittest.TestCase):
    def test_review_rejection_is_not_recovery_and_merge_holds_are_reported(self):
        helper = module('review_notification_tests', ROOT / 'release/review-notification.py')
        rejected = helper.actions('success', 'request_changes', 'skipped', '')
        self.assertIn(('model', '', True), rejected)
        self.assertTrue(any(kind=='review' and not resolved for kind,_,resolved in rejected))
        self.assertFalse(any(kind=='review' and resolved for kind,_,resolved in rejected))
        for result, decision in [('failure','approve'),('cancelled',''),('success','uncertain')]:
            with self.subTest(result=result,decision=decision):
                self.assertTrue(any(kind=='model' and not resolved for kind,_,resolved in helper.actions(result,decision,'skipped','')))
        for result, state in [('failure',''),('cancelled',''),('success','held'),('success','')]:
            with self.subTest(result=result,state=state):
                self.assertTrue(any(kind=='merge' and not resolved for kind,_,resolved in helper.actions('success','approve',result,state)))
        self.assertIn(('merge','',True),helper.actions('success','approve','success','merged'))
        self.assertEqual(helper.actions('success','skip','skipped',''),[])
        self.assertEqual(helper.actions('skipped','','skipped',''),[])

    def test_changed_candidate_reports_held_without_attempting_merge(self):
        helper = module('merge_outcome_tests', ROOT / 'release/merge-ai-review.py')
        sha = '1'*40
        review = {'decision':'approve','reviewed_sha':sha,'repository':'owner/repo','pr_number':1,'base_sha':'2'*40}
        cases = [({'state':'closed'},'already_closed'),
                 ({'state':'open','head':{'sha':'3'*40},'base':{'sha':'2'*40}},'held')]
        for pr, expected in cases:
            with self.subTest(expected=expected), tempfile.TemporaryDirectory() as work:
                path=Path(work)/'review.json';path.write_text(json.dumps(review))
                output=Path(work)/'outputs'
                with patch.object(helper.gate,'api',return_value=pr) as request, \
                     patch('sys.argv',['merge-ai-review.py','--repository','owner/repo','--expected-sha',sha,'--review',str(path)]), \
                     patch.dict(os.environ,{'GITHUB_OUTPUT':str(output)}):
                    helper.main()
                self.assertEqual(output.read_text(),'state='+expected+'\n')
                request.assert_called_once_with('owner/repo','pulls/1')

    def test_failure_issue_deduplicates_and_recovers_without_private_email_access(self):
        helper = module('notify_tests', ROOT / 'release/notify-failure.py')
        records = []; issues = []; comments = []
        def api(repo, path, method='GET', body=None):
            records.append((path, method, body))
            if path.startswith('issues?'):
                return issues
            if path == '':
                return {'owner': {'type': 'User', 'login': 'owner'}}
            if path == 'issues' and method == 'POST':
                issues.append(dict(body, number=1))
                return issues[0]
            if path.startswith('issues/1/comments?'):
                return comments
            if path == 'issues/1/comments' and method == 'POST':
                comments.append(body)
                return body
            if path == 'issues/1' and method == 'PATCH':
                issues[0].update(body)
                return issues[0]
            raise AssertionError(path)
        helper.notify('owner/repo','model','API failure','https://github.com/owner/repo/actions/runs/1',request=api)
        helper.notify('owner/repo','model','Retry still failed','https://github.com/owner/repo/actions/runs/2',request=api)
        helper.notify('owner/repo','model','Same run duplicate','https://github.com/owner/repo/actions/runs/2',request=api)
        self.assertEqual(sum(path=='issues' and method=='POST' for path,method,_ in records),1)
        self.assertEqual(len(comments),1)
        self.assertIn('@owner',comments[0]['body'])
        self.assertEqual(issues[0]['assignees'],['owner'])
        self.assertFalse(any('/user/emails' in path for path,_,_ in records))
        helper.notify('owner/repo','model','','',resolved=True,request=api)
        self.assertEqual(issues[0]['state'],'closed')

    def test_partial_issue_update_failure_does_not_duplicate_notification_comment(self):
        helper = module('notify_partial_tests', ROOT / 'release/notify-failure.py')
        issue = {'title':'[Upstream automation] 模型审核拒绝合并','number':1,'body':'old alert'}
        comments=[];fail=[True]
        def api(repo,path,method='GET',body=None):
            if path.startswith('issues?'):return [issue]
            if path=='':return {'owner':{'type':'User','login':'owner'}}
            if path.startswith('issues/1/comments?'):return comments
            if path=='issues/1/comments' and method=='POST':comments.append(body);return body
            if path=='issues/1' and method=='PATCH':
                if fail[0]:fail[0]=False;raise RuntimeError('simulated issue update outage')
                issue.update(body);return issue
            raise AssertionError(path)
        with self.assertRaises(RuntimeError):
            helper.notify('owner/repo','review','rejected','https://github.com/owner/repo/actions/runs/1',request=api)
        helper.notify('owner/repo','review','rejected','https://github.com/owner/repo/actions/runs/1',request=api)
        self.assertEqual(len(comments),1)


if __name__ == '__main__':
    unittest.main()
