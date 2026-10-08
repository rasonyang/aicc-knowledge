import json,time,urllib.request,statistics,sys
U=sys.argv[1] if len(sys.argv)>1 else "http://localhost:18080/embed"
Q=["How do I reset my password?","我想查询一下我的账单","What are your business hours on weekends?","请问怎么申请退款?","My internet connection keeps dropping, can you help?","我的订单什么时候能送到?","I want to cancel my subscription and get a refund.","如何修改我绑定的手机号码?","Can I speak to a human agent please?","发票可以开具增值税专用发票吗?","What is the status of my claim number 12345?","你们的客服电话是多少?","How do I upgrade my plan to the premium tier?","账户被锁定了怎么办,需要提供什么材料来解锁?","Is there a fee for international roaming when I travel to Japan next month?"]
def post(inp):
    d=json.dumps({"inputs":inp}).encode()
    r=urllib.request.Request(U,d,{"Content-Type":"application/json"})
    t=time.perf_counter(); b=urllib.request.urlopen(r).read(); return (time.perf_counter()-t)*1000,b
for i in range(10): post(Q[i%len(Q)])
L=[post(Q[i%len(Q)])[0] for i in range(200)]
L.sort()
p=lambda q:L[min(len(L)-1,int(round(q/100*len(L)))-1)]
print("single n=200 p50=%.1f p90=%.1f p99=%.1f max=%.1f mean=%.1f min=%.1f"%(statistics.median(L),p(90),p(99),L[-1],statistics.mean(L),L[0]))
B=[]
for i in range(20): B.append(post(Q[:8])[0])
B.sort(); print("batch8 n=20 p50=%.1f max=%.1f"%(statistics.median(B),B[-1]))
