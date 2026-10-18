"""Offline source oracle only. Never imported by the Go product."""
import ast
import json
from pathlib import Path
from types import SimpleNamespace
from typing import Optional, Tuple

root = Path(__file__).resolve().parent
tree = ast.parse((root / '__init__.py').read_text())
source = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == 'BrushFlowLanduo')
names = {'_calculate_global_downloader_size', '__bytes_to_gb', '__evaluate_size_condition_for_brush'}
body = [n for n in source.body if isinstance(n, ast.FunctionDef) and n.name in names]
harness = ast.ClassDef(name='BrushFlowLanduo', bases=[], keywords=[], body=body, decorator_list=[])
namespace = {'Optional': Optional, 'Tuple': Tuple, 'DownloaderHelper': None}
exec(compile(ast.fix_missing_locations(ast.Module(body=[harness], type_ignores=[])), str(root/'__init__.py'), 'exec'), namespace)

gib = 1024**3
cases = []
for task_limit in [None, 0, 1, 1.5]:
    for total in [0, gib-1, gib, int(1.5*gib), 2*gib]:
        cases.append({'name': f'task-{task_limit}-size-{total}', 'task_limit': task_limit,
                      'task_size': total, 'global_limit': 2, 'global_size': 2*gib-1})
cases += [
    {'name': 'missing-task', 'task_missing': True},
    {'name': 'global-boundary', 'global_limit': 2, 'global_size': 2*gib},
    {'name': 'no-limits', 'task_size': 100*gib},
    {'name': 'dedup-entire-downloader', 'global_limit': 2, 'tasks': ['qb', 'qb', '', 'tr'],
     'downloaders': {'qb': {'sizes': [gib, gib/2]}, 'tr': {'sizes': [gib/2]}}},
    {'name': 'empty-downloaders', 'global_limit': 1, 'tasks': ['qb'], 'downloaders': {'qb': {'sizes': []}}},
    {'name': 'missing-downloader', 'global_limit': 1, 'tasks': ['missing'], 'downloaders': {}},
    {'name': 'inactive-downloader', 'global_limit': 1, 'tasks': ['qb'], 'downloaders': {'qb': {'inactive': True}}},
    {'name': 'failed-downloader', 'global_limit': 1, 'tasks': ['qb'], 'downloaders': {'qb': {'error': True}}},
    {'name': 'supplied-snapshot-avoids-read', 'global_limit': 1, 'global_size': 0, 'tasks': ['missing']},
    {'name': 'task-limit-precedes-global-read', 'task_limit': 1, 'task_size': gib, 'global_limit': 1, 'tasks': ['missing']},
]
for case in cases:
    calls = []
    definitions = case.get('downloaders', {})
    class Helper:
        def get_service(self, name):
            calls.append(name)
            if name not in definitions: return None
            item = definitions[name]
            return SimpleNamespace(instance=SimpleNamespace(
                is_inactive=lambda: item.get('inactive', False),
                get_torrents=lambda: ([{'total_size': n} for n in item.get('sizes', [])], item.get('error', False)),
            ))
        @staticmethod
        def is_downloader(name, service=None): return True
    namespace['DownloaderHelper'] = Helper
    machine = namespace['BrushFlowLanduo']()
    machine._task_configs = {str(i): SimpleNamespace(downloader=name) for i, name in enumerate(case.get('tasks', []))}
    machine._global_disksize = case.get('global_limit')
    machine._get_task_config = lambda: None if case.get('task_missing') else SimpleNamespace(disksize=case.get('task_limit'))
    try:
        passed, reason = machine._BrushFlowLanduo__evaluate_size_condition_for_brush(case.get('task_size', 0), case.get('global_size'))
        case['expected'] = {'passed': passed, 'reason': reason, 'calls': sorted(calls)}
    except RuntimeError as err:
        case['expected'] = {'error': str(err), 'calls': sorted(calls)}
(root/'size-oracle.json').write_text(json.dumps({'scope': 'original AST three business functions under controlled read-only host fixtures; not actual downloaders or full BrushFlow', 'cases': cases}, ensure_ascii=False, indent=2)+'\n')
print(json.dumps({'source_cases': len(cases)}))
