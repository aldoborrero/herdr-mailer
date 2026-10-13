"""Read-only Herdr probe and disposable mailer/Git experiments; no model calls."""
import hashlib
import json
import os
import pathlib
import socket
import subprocess
import tempfile
import time
import uuid

ROOT = pathlib.Path(tempfile.mkdtemp(prefix='herdr-supervisor-probe-'))
EVIDENCE = ROOT / 'evidence'
EVIDENCE.mkdir()
MAILER = os.environ.get('HERDR_MAILER_BIN', 'herdr-mailer')
result = {'tests': {}, 'mailer_binary': MAILER}

def record(name, condition, **details):
    assert condition, (name, details)
    result['tests'][name] = {'pass': True, **details}

def rpc(path, request):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(3)
        s.connect(str(path))
        s.sendall(json.dumps(request).encode() + b'\n')
        return json.loads(s.makefile('rb').readline())

if os.environ.get('HERDR_ENV') != '1':
    raise SystemExit('Read-only Herdr inspection requires HERDR_ENV=1')
herdr_socket = os.environ.get('HERDR_SOCKET_PATH', os.path.expanduser('~/.config/herdr/herdr.sock'))
ping = rpc(herdr_socket, {'id': 'study-ping', 'method': 'ping', 'params': {}})
record('herdr_live_protocol', ping.get('result', {}).get('protocol') == 22, response=ping)
with socket.socket(socket.AF_UNIX) as s:
    s.settimeout(1)
    s.connect(herdr_socket)
    s.sendall(b'{"id":"study-eof","method":"ping","params":{}}\n')
    f = s.makefile('rb')
    assert f.readline()
    record('herdr_one_request_per_connection', f.readline() == b'')

observations = []
for subscriptions in [
    [{'type': 'pane.created'}],
    [{'type': 'pane.agent_status_changed', 'pane_id': os.environ['HERDR_PANE_ID']}],
]:
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(.5)
        s.connect(herdr_socket)
        s.sendall(json.dumps({'id':'study-events', 'method':'events.subscribe', 'params':{'subscriptions':subscriptions}}).encode()+b'\n')
        f = s.makefile('rb')
        ack = json.loads(f.readline())
        record('subscribe_' + subscriptions[0]['type'], ack.get('result', {}).get('type') == 'subscription_started')
        frames = []
        try:
            for _ in range(128):
                line = f.readline()
                if not line:
                    break
                frame = json.loads(line)
                # Never persist pane text, names or unrelated paths.
                frames.append({'event':frame.get('event'), 'data_keys':sorted(frame.get('data', {}).keys())})
        except (TimeoutError, socket.timeout):
            pass
        observations.append({'subscription':subscriptions[0]['type'], 'frames':frames})
result['herdr_event_observations'] = observations

state = pathlib.Path(tempfile.mkdtemp(prefix='mailer-state-', dir=ROOT))
def cli(*args):
    return subprocess.check_output([MAILER, '--state', str(state), *args], text=True)
cli('init')
owner = json.loads((state/'owner.json').read_text())
daemon = None
def start():
    p = subprocess.Popen([MAILER, '--state', str(state), 'daemon'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for _ in range(100):
        if (state/'daemon.sock').exists():
            return p
        if p.poll() is not None:
            raise AssertionError('daemon exited')
        time.sleep(.02)
    p.terminate(); p.wait(timeout=5)
    raise AssertionError('no socket')
def call(op, token, **kwargs):
    r = rpc(state/'daemon.sock', {'op':op, 'token':token, **kwargs})
    if 'error' in r:
        raise AssertionError(r)
    return r['data']
def events(token, cursor=0):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(3)
        s.connect(str(state/'daemon.sock'))
        s.sendall(json.dumps({'op':'events', 'token':token, 'cursor':cursor}).encode()+b'\n')
        f = s.makefile('rb')
        frames = []
        while True:
            line = f.readline()
            assert line
            data = json.loads(line)['data']
            if data.get('done'):
                return frames
            frames.append(data)
try:
    daemon = start()
    # Administrative material stays private in this disposable test state.
    admin = owner.get('adminToken') or owner.get('admin_token') or owner.get('admin')
    assert admin, list(owner)
    sender = call('register', admin, agent='control', runtime='none')
    worker = call('register', admin, agent='worker', runtime='none')
    body = json.dumps({'operation_id':str(uuid.uuid4()), 'type':'wake', 'revision':1})
    a = call('send', sender['token'], recipient='worker', body=body)
    b = call('send', sender['token'], recipient='worker', body=body)
    record('send_retry_is_not_idempotent_today', a['id'] != b['id'], distinct_messages=2)
    first = events(worker['token'])
    record('recipient_events_exist', len(first) >= 2)
    cursor = max(e['cursor'] for e in first)
    record('cursor_replay_has_no_previous_events', events(worker['token'], cursor) == [])
    call('send', sender['token'], recipient='worker', body='next')
    tail = events(worker['token'], cursor)
    record('cursor_sees_later_event', any(e['cursor'] > cursor for e in tail))
    daemon.terminate(); daemon.wait(timeout=5); daemon = None
    daemon = start()
    after = events(worker['token'], cursor)
    record('events_and_credentials_survive_daemon_restart', after == tail)
    rotated = call('register', admin, agent='worker', runtime='none')
    record('register_is_rotation_not_resume', rotated['session']['epoch'] == worker['session']['epoch'] + 1 and rotated['session']['id'] != worker['session']['id'])
    old = rpc(state/'daemon.sock', {'op':'inbox', 'token':worker['token']})
    record('rotation_revokes_old_token', bool(old.get('error')))
    # Original envelopes retain their pinned destination after rotation.
    record('envelope_pins_recipient_session', a['recipientSession'] == worker['session']['id'] and a['recipientEpoch'] == worker['session']['epoch'])
finally:
    if daemon is not None:
        daemon.terminate(); daemon.wait(timeout=5)

repo = pathlib.Path(tempfile.mkdtemp(prefix='git-cas-', dir=ROOT))
env = dict(os.environ, GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL='/dev/null')
def git(*args, check=True):
    return subprocess.run(['git', '-C', str(repo), '-c', 'user.name=Feasibility', '-c', 'user.email=study@localhost', '-c', 'commit.gpgsign=false', *args], env=env, check=check, capture_output=True, text=True)
git('init', '-b', 'main')
(repo/'base').write_text('base\n'); git('add', '.'); git('commit', '-m', 'base')
base = git('rev-parse', 'HEAD').stdout.strip()
git('branch', 'run-integration', base)
git('checkout', '-b', 'worker')
(repo/'feature').write_text('feature\n'); git('add', '.'); git('commit', '-m', 'candidate')
candidate = git('rev-parse', 'HEAD').stdout.strip()
git('checkout', 'main')
(repo/'concurrent').write_text('concurrent\n'); git('add', '.'); git('commit', '-m', 'base moved')
moved = git('rev-parse', 'HEAD').stdout.strip()
failed = git('update-ref', 'refs/heads/main', candidate, base, check=False)
record('git_stale_base_cas_rejected', failed.returncode != 0 and git('rev-parse', 'main').stdout.strip() == moved)
git('update-ref', 'refs/heads/run-integration', candidate, base)
record('git_owned_integration_ref_cas_succeeds', git('rev-parse', 'run-integration').stdout.strip() == candidate and git('rev-parse', 'main').stdout.strip() == moved)
result['scope'] = 'No agents spawned; read-only live Herdr; isolated mailer runtime=none and disposable Git repository. No native model completion tested.'
(EVIDENCE/'protocol-probes.json').write_text(json.dumps(result, indent=2)+'\n')
print(json.dumps(result, indent=2))
