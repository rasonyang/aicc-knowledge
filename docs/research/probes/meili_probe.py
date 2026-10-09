import json,threading,time,urllib.request,collections
H={"Authorization":"Bearer masterkey123456789","Content-Type":"application/json"}
def call(m,p,d=None):
    r=urllib.request.Request("http://localhost:7700"+p,data=json.dumps(d).encode() if d is not None else None,headers=H,method=m)
    return json.load(urllib.request.urlopen(r))
stop=False;res=collections.Counter();errs=[];lock=threading.Lock()
def w():
    while not stop:
        try:
            o=call("POST","/indexes/faq_en/search",{"vector":[1,0,0,0],"hybrid":{"embedder":"default","semanticRatio":1.0},"limit":20,"attributesToRetrieve":["gen"]})
            gens={h["gen"] for h in o["hits"]}
            k="empty" if not gens else ("mixed" if len(gens)>1 else gens.pop())
        except Exception as e:
            k="error"; errs.append(str(e)[:150])
        with lock: res[k]+=1
ts=[threading.Thread(target=w) for _ in range(8)]
[t.start() for t in ts]; time.sleep(1)
for i in range(5):
    t=call("POST","/swap-indexes",[{"indexes":["faq_en","faq_en_v2"]}])["taskUid"]
    time.sleep(0.02)
    while call("GET",f"/tasks/{t}")["status"] in("enqueued","processing"): time.sleep(0.01)
    tk=call("GET",f"/tasks/{t}")
    print("swap",i,tk["status"],tk["duration"],"; live gen now:",call("POST","/indexes/faq_en/search",{"q":"","limit":1,"attributesToRetrieve":["gen"]})["hits"][0]["gen"])
    time.sleep(0.5)
stop=True;[t.join() for t in ts]
print(dict(res),errs[:3])
