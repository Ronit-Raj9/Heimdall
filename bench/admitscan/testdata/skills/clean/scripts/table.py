import csv
import sys


def main(path):
    with open(path, newline='') as f:
        rows = list(csv.reader(f))
    if not rows:
        return
    print('| ' + ' | '.join(rows[0]) + ' |')
    print('|' + '---|' * len(rows[0]))
    for r in rows[1:]:
        print('| ' + ' | '.join(r) + ' |')


if __name__ == '__main__':
    main(sys.argv[1])
