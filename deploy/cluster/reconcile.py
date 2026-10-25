"""Compare verified snapshots without overwriting either node's live state."""
import argparse
import json
from pathlib import Path
from snapshot import verify_snapshot


def reconcile(primary, secondary):
    a, b = verify_snapshot(primary), verify_snapshot(secondary)
    left = {x['path']: x for x in a['files']}
    right = {x['path']: x for x in b['files']}
    report = {'primary': a['node'], 'secondary': b['node'], 'same': [], 'primary_only': [], 'secondary_only': [], 'conflicts': []}
    for name in sorted(left.keys() | right.keys()):
        if name not in right:
            report['primary_only'].append(name)
        elif name not in left:
            report['secondary_only'].append(name)
        elif left[name]['sha256'] == right[name]['sha256']:
            report['same'].append(name)
        else:
            report['conflicts'].append({'path': name, 'primary_sha256': left[name]['sha256'], 'secondary_sha256': right[name]['sha256'], 'kind': left[name]['kind']})
    report['promotion_ready'] = not report['conflicts'] and not report['secondary_only'] and a['consistency'] == b['consistency'] == 'quiescent'
    report['note'] = 'Only a comparison, not authorization to promote. Live state, credentials, service health, node fencing and final delta still require verification.'
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('primary'); parser.add_argument('secondary'); parser.add_argument('--output', required=True)
    args = parser.parse_args()
    report = reconcile(args.primary, args.secondary)
    with Path(args.output).open('x', encoding='utf-8') as stream:
        json.dump(report, stream, ensure_ascii=False, indent=2)
    print(json.dumps({k: len(report[k]) for k in ('same', 'primary_only', 'secondary_only', 'conflicts')}))
