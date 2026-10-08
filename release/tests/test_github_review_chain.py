"""CLI-level local simulation of GitHub's repository-root routing."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
FAKE_GH = '''#!/usr/bin/env python3
import json,os,sys
args=sys.argv[1:]; path=args[1]
method=args[args.index('--method')+1] if '--method' in args else 'GET'
body=json.load(sys.stdin) if '--input' in args else None
with open(os.environ['FAKE_GITHUB_LOG'],'a') as log:
    log.write(json.dumps({'path':path,'method':method,'body':body})+'\\n')
if path=='repos/owner/repo':
    result={'default_branch':'main','owner':{'type':'User','login':'owner'}}
elif path.startswith('repos/owner/repo/commits/') and path.endswith('/pulls'):
    result=[]
elif path.startswith('repos/owner/repo/issues?'):
    result=[]
elif path=='repos/owner/repo/issues' and method=='POST':
    result=dict(body,number=1)
elif path=='repos/owner/repo/actions/workflows/build.yml':
    result={'id':123}
else:
    print('Not Found (HTTP 404)',file=sys.stderr);sys.exit(1)
print(json.dumps(result))
'''


class ReviewChainTests(unittest.TestCase):
    def test_non_candidate_skips_notification_posts_and_gate_remains_read_only(self):
        with tempfile.TemporaryDirectory() as work:
            folder = Path(work)
            cli = folder / 'gh'; cli.write_text(FAKE_GH); cli.chmod(0o755)
            log = folder / 'requests.jsonl'
            output = folder / 'review.json'
            env = dict(os.environ, PATH=work + os.pathsep + os.environ['PATH'],
                       FAKE_GITHUB_LOG=str(log), UPSTREAM_REVIEWER_LOGINS='review-bot',
                       UPSTREAM_NOTIFICATION_MODE='issue')
            commands = [
                ['ai-review.py', '--repository', 'owner/repo', '--sha', '1'*40, '--output', str(output)],
                ['notify-failure.py', '--repository', 'owner/repo', '--kind', 'model',
                 '--reason', 'Test outage', '--run-url', 'https://github.com/owner/repo/actions/runs/1'],
                ['review-gate.py', '--repository', 'owner/repo', '--sha', '1'*40],
            ]
            for script, *args in commands:
                result = subprocess.run(['python3', str(ROOT/'release'/script), *args],
                                        env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                self.assertEqual(result.returncode, 0, result.stderr.decode())
            self.assertEqual(json.loads(output.read_text())['decision'], 'skip')
            records = [json.loads(line) for line in log.read_text().splitlines()]
            posts = [r for r in records if r['method']!='GET']
            self.assertEqual(len(posts), 1)
            self.assertEqual(posts[0]['path'], 'repos/owner/repo/issues')
            self.assertIn('@owner', posts[0]['body']['body'])
            self.assertEqual(posts[0]['body']['assignees'], ['owner'])
            self.assertFalse(any(r['path'].endswith('/') or '/merge' in r['path'] or '/user/emails' in r['path'] for r in records))


if __name__ == '__main__':
    unittest.main()
