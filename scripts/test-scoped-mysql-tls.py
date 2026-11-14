import os, subprocess, tempfile, time, uuid
from pathlib import Path

run = lambda args, **kw: subprocess.run(args, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, **kw)
names=[]
with tempfile.TemporaryDirectory(prefix="df-scoped-tls-") as td:
 p=Path(td); os.chmod(p,0o755)
 try:
  run(["openssl","req","-x509","-newkey","rsa:2048","-nodes","-keyout",str(p/"ca.key"),"-out",str(p/"ca.pem"),"-days","1","-subj","/CN=Synthetic CA"])
  for label,ip in [("good","::1"),("bad","::2")]:
   run(["openssl","req","-newkey","rsa:2048","-nodes","-keyout",str(p/(label+".key")),"-out",str(p/(label+".csr")),"-subj","/CN=synthetic"])
   (p/(label+".cnf")).write_text("subjectAltName=IP:"+ip+"\n")
   run(["openssl","x509","-req","-in",str(p/(label+".csr")),"-CA",str(p/"ca.pem"),"-CAkey",str(p/"ca.key"),"-CAcreateserial","-out",str(p/(label+".pem")),"-days","1","-extfile",str(p/(label+".cnf"))])
   os.chmod(p/(label+".key"),0o644); os.chmod(p/(label+".pem"),0o644)
  os.chmod(p/"ca.pem",0o644)
  ports=[]
  for label in ["good","bad"]:
   name="df-scoped-test-"+label+"-"+uuid.uuid4().hex[:8]; names.append(name)
   run(["docker","run","--rm","-d","--name",name,"-p","[::1]::3306","-v",td+":/certs:ro","-e","MYSQL_ROOT_PASSWORD=synthetic-pass","-e","MYSQL_DATABASE=synthetic","mysql:8.4.13","--ssl-ca=/certs/ca.pem","--ssl-cert=/certs/"+label+".pem","--ssl-key=/certs/"+label+".key","--require_secure_transport=ON"])
   port=subprocess.check_output(["docker","port",name,"3306/tcp"],text=True).strip().rsplit(":",1)[1]; ports.append(port)
  for name in names:
   for i in range(90):
    r=subprocess.run(["docker","exec",name,"mysqladmin","ping","-h","127.0.0.1","-uroot","-psynthetic-pass","--ssl-mode=REQUIRED"],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    if r.returncode==0: break
    time.sleep(1)
   else: raise RuntimeError("synthetic mysql startup timeout")
  env=os.environ.copy(); env.update({"DF_SCOPED_TLS_CA":str(p/"ca.pem"),"DF_SCOPED_TLS_GOOD_PORT":ports[0],"DF_SCOPED_TLS_BAD_PORT":ports[1]})
  subprocess.run(["go","test","./internal/database/drivers/traditional","-run","^TestScopedMySQLTLSRealDatabase$","-count=1","-v"],env=env,check=True)
 finally:
  for name in names: subprocess.run(["docker","stop",name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
