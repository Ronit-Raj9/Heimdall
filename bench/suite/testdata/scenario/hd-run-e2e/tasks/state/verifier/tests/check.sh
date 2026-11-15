python3 -c "import json, sys; sys.exit(0 if json.load(open('/grader/state/db/state')).get('done') is True else 1)"
