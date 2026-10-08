H='Authorization: Bearer masterkey123456789'
B=localhost:7700
req(){ m=$1; p=$2; d=$3; curl -s -X $m "$B$p" -H "$H" -H 'Content-Type: application/json' ${d:+-d "$d"}; echo; }
wait_task(){ u=$1; while true; do s=$(curl -s $B/tasks/$u -H "$H"); st=$(echo "$s"|python3 -c 'import sys,json;print(json.load(sys.stdin)["status"])'); [ "$st" = enqueued -o "$st" = processing ] || { echo "$s"; break; }; sleep 0.2; done; }
tid(){ python3 -c 'import sys,json;print(json.load(sys.stdin)["taskUid"])'; }
