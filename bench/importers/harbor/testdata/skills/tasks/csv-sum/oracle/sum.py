import csv
rows = list(csv.DictReader(open("/app/data.csv")))
open("/app/total.txt", "w").write(str(sum(int(r["amount"]) for r in rows)))
