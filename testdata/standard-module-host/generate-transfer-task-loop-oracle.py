"""Replay the pinned manual per-task loop, with a controlled physical task
boundary. The actual loop determines continuation, error aggregation and
progress; this is not a full MoviePilot service run or storage-module test.
"""
import ast, copy, json, subprocess
from pathlib import Path
from types import SimpleNamespace

commit = '8a83efeacd3e26b82be495e17d50146e31c96a60'
source = subprocess.check_output(['git', '-C', '/Users/accidia/projects/MoviePilot', 'show', commit + ':app/chain/transfer.py'], text=True)
tree = ast.parse(source)
loop = next(n for n in ast.walk(tree) if isinstance(n, ast.For) and ast.unparse(n.target) == 'transfer_task' and ast.unparse(n.iter) == 'transfer_tasks')
fn = ast.FunctionDef(name='replay', args=ast.arguments(posonlyargs=[], args=[ast.arg(arg='self')], kwonlyargs=[], kw_defaults=[], defaults=[]), body=[copy.deepcopy(loop), ast.Return(value=ast.Tuple(elts=[ast.Name(id=k, ctx=ast.Load()) for k in ['all_success', 'processed_num', 'fail_num', 'err_msgs', 'finished_files']], ctx=ast.Load()))], decorator_list=[])
prefix = [ast.parse('all_success=True\nprocessed_num=0\nfail_num=0\nerr_msgs=[]\nfinished_files=[]').body]
fn.body = prefix[0] + fn.body
rows = []
for name, outcomes in [('subtitle_false', [True, False, True]), ('main_false', [False, True, True]), ('subtitle_exception', [True, 'throw', True]), ('two_failures', [False, False, True])]:
    trace = []
    tasks = [SimpleNamespace(fileitem=SimpleNamespace(name=n, path='/owned/' + n), outcome=v) for n, v in zip(['Owned.mkv', 'Owned.srt', 'Owned.flac'], outcomes)]
    def handle(*, task, callback):
        trace.append('handle:' + task.fileitem.name)
        if task.outcome == 'throw':
            raise RuntimeError('owned failure')
        return task.outcome, '' if task.outcome else 'owned rejection'
    host = SimpleNamespace(__start_job_execution=lambda task: trace.append('start:' + task.fileitem.name), __handle_transfer=handle, __default_callback=None, __fail_transfer_task=lambda task: trace.append('fail:' + task.fileitem.name), __finish_job_execution=lambda task: trace.append('finish:' + task.fileitem.name))
    ns = dict(transfer_tasks=tasks, global_vars=SimpleNamespace(is_system_stopped=False), continue_callback=None, preview=False, total_num=len(tasks), Path=Path, progress=SimpleNamespace(update=lambda **kw: None), logger=SimpleNamespace(info=lambda *a: None, warn=lambda *a: None, error=lambda *a: None), traceback=SimpleNamespace(format_exc=lambda: 'controlled task error'))
    exec(compile(ast.fix_missing_locations(ast.Module(body=[fn], type_ignores=[])), '<fixed:manual task loop>', 'exec'), ns)
    success, completed, failed, errors, finished = ns['replay'](host)
    rows.append(dict(name=name, outcomes=outcomes, success=success, completed=completed, failed=failed, errors=errors, finished=finished, trace=trace))
Path(__file__).with_name('transfer-task-loop-source-oracle.json').write_text(json.dumps(dict(fixed_backend=commit, source='app/chain/transfer.py:3588-3661', kind=__doc__, cases=rows), ensure_ascii=False, indent=2) + '\n')
print(len(rows), 'fixed manual continuation cases')
