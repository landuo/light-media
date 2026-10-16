"""Execute cached installed functions with recording dependencies, no app import."""
import ast
import copy
import datetime
import enum
import inspect
import json
import pathlib
import types

ROOT = pathlib.Path(__file__).resolve().parent
SOURCE = json.loads((ROOT / 'installed-source.json').read_text())


def segment(path, name):
    return next(s for s in SOURCE['files'][path]['segments'] if s['name'] == name)


class StripAnnotations(ast.NodeTransformer):
    def visit_arg(self, node):
        node.annotation = None
        return node

    def visit_FunctionDef(self, node):
        node.decorator_list = []
        node.returns = None
        self.generic_visit(node)
        return node


def load(path, name, namespace):
    tree = StripAnnotations().visit(ast.parse(segment(path, name)['source']))
    exec(compile(ast.fix_missing_locations(tree), '<installed-source-only>', 'exec'), namespace)
    return namespace[name]


class Logger:
    def __getattr__(self, name):
        return lambda *args, **kwargs: None


def encode(value):
    if isinstance(value, enum.Enum):
        return value.value
    if isinstance(value, dict):
        return {k: encode(v) for k, v in value.items()}
    if isinstance(value, (tuple, list)):
        return [encode(v) for v in value]
    return value


types_ns = {'Enum': enum.Enum}
load('schemas/types.py', 'EventType', types_ns)
load('schemas/types.py', 'NotificationType', types_ns)
EventType, NotificationType = types_ns['EventType'], types_ns['NotificationType']

download_fn = load('chain/download.py', 'download_file_deleted', {
    'logger': Logger(), 'EventType': EventType,
})
cases = []
for name, event_data, torrents, remove_result in [
    ('no-event', None, [], True),
    ('src-only-file', {'src': '/synthetic/movie.mkv'}, [], True),
    ('src-only-directory', {'src': '/synthetic'}, [], True),
    ('empty-hash-with-src', {'src': '/synthetic/movie.mkv', 'hash': ''}, [], True),
    ('hash-no-torrent', {'hash': 'synthetic-hash'}, [], True),
    ('hash-one-torrent', {'hash': 'synthetic-hash'}, [{'hash': 'synthetic-hash', 'downloader': 'fixture-qb'}], True),
    ('hash-remove-false-still-event', {'hash': 'synthetic-hash', 'src': '/synthetic/movie.mkv'}, [{'hash': 'synthetic-hash'}], False),
    ('hash-multiple-torrents', {'hash': 'synthetic-hash'}, [{'hash': 'synthetic-hash', 'downloader': 'fixture-qb'}, {'hash': 'synthetic-hash', 'downloader': 'fixture-tr'}], True),
]:
    trace = []

    def list_torrents(**kwargs):
        trace.append({'op': 'list_torrents', **kwargs})
        return [types.SimpleNamespace(model_dump=lambda row=row: row) for row in torrents]

    def remove_torrents(**kwargs):
        trace.append({'op': 'remove_torrents', **kwargs})
        return remove_result

    subject = types.SimpleNamespace(
        list_torrents=list_torrents,
        remove_torrents=remove_torrents,
        eventmanager=types.SimpleNamespace(send_event=lambda kind, data: trace.append({'op': 'event', 'kind': kind.value, 'data': data})),
    )
    event = None if event_data is None else types.SimpleNamespace(event_data=event_data)
    download_fn(subject, event)
    if name.startswith('src-only') or name in ['empty-hash-with-src', 'no-event']:
        assert trace == [], (name, trace)
    if name == 'hash-remove-false-still-event':
        assert [t['op'] for t in trace] == ['list_torrents', 'remove_torrents', 'event']
        assert trace[1]['delete_file'] is False
    cases.append({'name': name, 'input': {'event': event_data, 'torrents': torrents, 'remove_result': remove_result}, 'trace': trace})

# Defaults are copied from the actual Notification AnnAssign values; construction
# is a recording stub, not a proof of Pydantic validation or enum serialization.
model_ast = ast.parse(segment('schemas/message.py', 'Notification')['source']).body[0]
defaults = {n.target.id: ast.literal_eval(n.value) for n in model_ast.body if isinstance(n, ast.AnnAssign)}


class Message:
    def __init__(self, **kwargs):
        self.__dict__.update(copy.deepcopy(defaults))
        self.__dict__.update(kwargs)

    def model_dump(self, exclude=None):
        return {k: copy.deepcopy(v) for k, v in self.__dict__.items() if k not in (exclude or set())}


message_cases = []
for mode in ['default', 'history-failure', 'event-failure', 'queue-call-failure', 'history-disabled', 'admin-route', 'explicit-user']:
    trace = []
    switch = 'admin' if mode == 'admin-route' else None

    def render(message, **kwargs):
        trace.append({'op': 'render'})
        return message

    def history_add(**kwargs):
        if mode == 'history-failure':
            raise ValueError('synthetic history failure')
        trace.append({'op': 'history', 'data': kwargs})

    def send_event(**kwargs):
        if mode == 'event-failure':
            raise ValueError('synthetic event insertion failure')
        trace.append({'op': 'event', 'kind': kwargs['etype'], 'data': kwargs['data']})

    def send_message(*args, **kwargs):
        if mode == 'queue-call-failure':
            raise ValueError('synthetic send interface failure')
        trace.append({'op': 'send_interface', 'method': args[0], 'immediately': kwargs.get('immediately', False), 'data': kwargs['message'].model_dump()})

    ns = {
        'copy': copy, 'logger': Logger(), 'EventType': EventType,
        'datetime': types.SimpleNamespace(now=lambda: datetime.datetime(2026, 1, 2, 12, 0, 0)),
        'MessageTemplateHelper': types.SimpleNamespace(render=render),
        'ServiceConfigHelper': types.SimpleNamespace(get_notification_switch=lambda _: switch),
        'settings': types.SimpleNamespace(SUPERUSER='synthetic-admin'),
        'UserOper': lambda: types.SimpleNamespace(get_settings=lambda username: {'fixture': 'synthetic-admin-target'}),
    }
    fn = load('chain/__init__.py', 'post_message', ns)
    normalize = load('chain/__init__.py', '_normalize_notification_for_dispatch', {'copy': copy, 'normalize_internal_user_id': lambda v: v})
    build_event = load('chain/__init__.py', '_build_notice_message_data', {})
    subject = types.SimpleNamespace(
        messageoper=types.SimpleNamespace(add=history_add),
        eventmanager=types.SimpleNamespace(send_event=send_event),
        messagequeue=types.SimpleNamespace(send_message=send_message),
        _normalize_notification_for_dispatch=normalize,
        _build_notice_message_data=build_event,
    )
    message = Message(mtype=NotificationType.SiteMessage, title='🧹 媒体文件清理', text='synthetic-only')
    if mode == 'history-disabled':
        message.save_history = False
    if mode == 'explicit-user':
        message.userid = 'synthetic-user'
    error = None
    try:
        fn(subject, message)
    except Exception as e:
        error = {'class': type(e).__name__, 'message': str(e)}
    expected = {
        'default': ['render', 'history', 'event', 'send_interface'],
        'history-failure': ['render'],
        'event-failure': ['render', 'history'],
        'queue-call-failure': ['render', 'history', 'event'],
        'history-disabled': ['render', 'event', 'send_interface'],
        'admin-route': ['render', 'history', 'event', 'send_interface'],
        'explicit-user': ['render', 'history', 'event', 'send_interface'],
    }[mode]
    assert [t['op'] for t in trace] == expected
    for t in trace:
        if t['op'] == 'event':
            assert 'save_history' not in t['data']
            assert t['data']['type'] == t['data']['mtype'] == NotificationType.SiteMessage
    message_cases.append({'name': mode, 'trace': encode(trace), 'error': error})

base_trace = []
post = load('plugins/__init__.py', 'post_message', {
    'Notification': Message,
    'settings': types.SimpleNamespace(MP_DOMAIN=lambda path: 'https://synthetic.invalid/' + path),
})
Base = type('RemoveLink', (), {'post_message': post})
base = Base()
base.chain = types.SimpleNamespace(post_message=lambda msg: base_trace.append(msg.model_dump()))
base.post_message(mtype=NotificationType.SiteMessage, title='synthetic-title', text='synthetic-text')
assert base_trace[0]['save_history'] is True
assert base_trace[0]['link'].endswith('#/plugins?tab=installed&id=RemoveLink')
assert base_trace[0]['source'] is None and base_trace[0]['userid'] is None

# Execute the actual broadcast enqueue function: no consumer executes inline.
enqueued = []
enqueue = load('core/event.py', '__trigger_broadcast_event', {'logger': Logger()})
event = types.SimpleNamespace(priority=10, event_data={'src': '/synthetic/movie.mkv'})
manager = types.SimpleNamespace(__event_queue=types.SimpleNamespace(put=lambda value: enqueued.append(value)))
returned = enqueue(manager, event)
assert returned is None and enqueued == [(10, event)]

output = {
    'execution': 'actual installed cached function AST; recording-only dependencies; no original app import/DB/network/notification; Notification constructor/render and message sender are stubs, not external delivery proofs',
    'source_sha256': {p: v.get('sha256') for p, v in SOURCE['files'].items()},
    'download_cases': cases,
    'notification_cases': message_cases,
    'plugin_notification_defaults': encode(base_trace[0]),
    'broadcast_enqueue': {'return': returned, 'queued': len(enqueued), 'priority': 10, 'handlers_called_inline': 0},
}
(ROOT / 'installed-function-oracle.json').write_text(json.dumps(output, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({'download_cases': len(cases), 'notification_cases': len(message_cases), 'plugin_defaults': 1, 'broadcast_enqueue': 1}))
