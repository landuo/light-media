#!/usr/bin/env python3
"""Rewrite records(kind,id,payload,updated_at) access to the per-feature tables.

Rules are deliberately conservative: a literal is only rewritten when every kind
predicate resolves to one known table, otherwise it is reported for a manual
edit. The script prints a JSON report so the remaining sites can be tracked.
"""
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
GO = ROOT / "internal"


def load_kinds():
    src = (GO / "app" / "records_tables.go").read_text()
    block = src.split("var recordKinds = map[string]string{")[1].split("\n}")[0]
    kinds = dict(re.findall(r'"([a-z_0-9]+)":\s*"([a-z_0-9]+)"', block))
    mp = set(re.findall(r'^\t"([a-z_0-9]+)":\s*\{name:', src, re.M))
    return kinds, mp


KINDS, MP_TABLES = load_kinds()

LITERAL = re.compile(r"`[^`]*`|\"(?:[^\"\\]|\\.)*\"", re.S)


def kind_of(seg):
    found = set(re.findall(r"kind\s*=\s*'([a-z_0-9]+)'", seg))
    for grp in re.findall(r"kind\s+IN\s*\(([^)]*)\)", seg):
        found |= set(re.findall(r"'([a-z_0-9]+)'", grp))
    return found


def strip_kind_predicates(sql, kinds):
    for kind in kinds:
        sql = sql.replace("WHERE kind='%s' AND " % kind, "WHERE ")
        sql = sql.replace("WHERE kind='%s'" % kind, "WHERE 1=1")
        sql = sql.replace(" AND kind='%s'" % kind, "")
        sql = sql.replace("WHERE kind = '%s' AND " % kind, "WHERE ")
        sql = sql.replace("WHERE kind = '%s'" % kind, "WHERE 1=1")
        sql = sql.replace(" AND kind = '%s'" % kind, "")
        sql = sql.replace("(kind='%s' AND " % kind, "(")
        sql = sql.replace(" OR (kind='%s' AND " % kind, " OR (")
    return sql


def rewrite_key(sql, table):
    if table not in MP_TABLES:
        return sql
    # Only bare id references are the record key; JSON paths and *_id columns
    # stay untouched because they are preceded by '.', '$' or '_'.
    return re.sub(r"(?<![\w.$'])id(?![\w])", "uid", sql)


def convert(literal, kinds):
    body = literal[1:-1] if literal.startswith("`") else literal
    stripped = literal[1:-1] if literal.startswith("`") else literal
    tables = {KINDS[k] for k in kinds if k in KINDS}
    if len(tables) != 1:
        return None
    table = tables.pop()
    sql = strip_kind_predicates(stripped, kinds)
    sql = re.sub(r"\brecords\b", table, sql)
    sql = rewrite_key(sql, table)
    if literal.startswith("`"):
        return "`" + sql + "`"
    return '"' + sql.replace('"', '\\"') + '"'


def main(apply):
    report = {"rewritten": 0, "plugin_record": 0, "skipped": []}
    files = sorted(GO.rglob("*.go"))
    for path in files:
        text = path.read_text()
        if "records" not in text:
            continue
        out, last = [], 0
        for m in LITERAL.finditer(text):
            seg = m.group(0)
            if "records" not in seg:
                continue
            kinds = kind_of(seg)
            known = {k for k in kinds if k in KINDS}
            if known and len({KINDS[k] for k in known}) == 1:
                if "SELECT kind" in seg or re.search(r"\b(kind,|,kind)\b", seg):
                    report["skipped"].append(
                        {"file": str(path.relative_to(ROOT)), "line": text[: m.start()].count("\n") + 1,
                         "reason": "selects kind column", "sql": seg[:160]})
                    continue
                if re.search(r"ON records\b", seg) or "NEW.kind" in seg:
                    report["skipped"].append(
                        {"file": str(path.relative_to(ROOT)), "line": text[: m.start()].count("\n") + 1,
                         "reason": "legacy trigger on records", "sql": seg[:160]})
                    continue
                new = convert(seg, known)
                if new is None:
                    continue
                out.append(text[last:m.start()])
                out.append(new)
                last = m.end()
                report["rewritten"] += 1
            elif not known and re.search(r"(FROM|INTO|UPDATE)\s+records\b", seg) and "kind=?" not in seg and "kind = ?" not in seg:
                new = re.sub(r"\brecords\b", "plugin_record", seg)
                out.append(text[last:m.start()])
                out.append(new)
                last = m.end()
                report["plugin_record"] += 1
            else:
                report["skipped"].append(
                    {"file": str(path.relative_to(ROOT)), "line": text[: m.start()].count("\n") + 1,
                     "reason": "multi-table or dynamic kind", "sql": seg[:160]})
        if last:
            out.append(text[last:])
            new_text = "".join(out)
            if apply:
                path.write_text(new_text)
    print(json.dumps(report, indent=1, ensure_ascii=False)[:20000])
    print("skipped:", len(report["skipped"]))


if __name__ == "__main__":
    main(len(sys.argv) > 1 and sys.argv[1] == "--apply")
