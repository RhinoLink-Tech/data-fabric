#!/usr/bin/env python3
"""Disposable Keycloak/MCP acceptance test; requires Docker, Go, OpenSSL and requests."""
import contextlib,email,html,json,os,pathlib,re,secrets,shutil,subprocess,sys,tempfile,time,urllib.parse
from html.parser import HTMLParser
import requests

suffix=secrets.token_hex(4)
network='df603-net-'+suffix
smtp='df603-smtp-'+suffix
kc='df603-identity-'+suffix
proxy='df603-proxy-'+suffix
pg='df603-pg-'+suffix
admin='testadmin'
password=secrets.token_urlsafe(24)
env=os.environ.copy()
env.update(KC_BOOTSTRAP_ADMIN_USERNAME=admin,KC_BOOTSTRAP_ADMIN_PASSWORD=password,KC_PROXY_HEADERS='xforwarded',KC_HOSTNAME_STRICT='false')
created_network=False;created=[]
def run(args,**kwargs):
 return subprocess.run(args,capture_output=True,text=True,timeout=35,**kwargs)
class Parser(HTMLParser):
 def __init__(self):
  super().__init__();self.links=[];self.forms={}
 def handle_starttag(self,tag,attrs):
  a=dict(attrs)
  if tag=='a' and a.get('href'):self.links.append(a['href'])
  if tag=='form' and a.get('id'):self.forms[a['id']]=a.get('action')
try:
 with tempfile.TemporaryDirectory(prefix='df603-identity-') as td:
  spool=pathlib.Path(td)
  smtpcode='''import pathlib,socketserver
class Handler(socketserver.StreamRequestHandler):
 def handle(self):
  self.wfile.write(b"220 fake-smtp\\r\\n")
  while True:
   line=self.rfile.readline(8192)
   if not line:break
   cmd=line.upper()
   if cmd.startswith((b"EHLO",b"HELO")):self.wfile.write(b"250-fake\\r\\n250 8BITMIME\\r\\n")
   elif cmd.startswith((b"MAIL ",b"RCPT ",b"RSET")):self.wfile.write(b"250 OK\\r\\n")
   elif cmd.startswith(b"DATA"):
    self.wfile.write(b"354 End data\\r\\n")
    chunks=[]
    while True:
     l=self.rfile.readline(1048576)
     if not l or l==b".\\r\\n":break
     chunks.append(l)
    pathlib.Path("/work/message.eml").write_bytes(b"".join(chunks))
    self.wfile.write(b"250 OK\\r\\n")
   elif cmd.startswith(b"QUIT"):
    self.wfile.write(b"221 Bye\\r\\n");break
   else:self.wfile.write(b"250 OK\\r\\n")
class Server(socketserver.ThreadingTCPServer):allow_reuse_address=True
Server(("0.0.0.0",1025),Handler).serve_forever()
'''
  (spool/'smtp.py').write_text(smtpcode)
  cert=spool/'cert.pem';private=spool/'key.pem'
  openssl=run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(private),'-out',str(cert),'-days','1','-subj','/CN=127.0.0.1','-addext','subjectAltName=IP:127.0.0.1'])
  if openssl.returncode:raise RuntimeError('test TLS certificate')
  private.chmod(0o644)
  (spool/'nginx.conf').write_text('events {} http { server { listen 443 ssl; ssl_certificate /work/cert.pem; ssl_certificate_key /work/key.pem; location / { proxy_pass http://identity:8080; proxy_set_header Host $http_host; proxy_set_header X-Forwarded-Proto https; proxy_set_header X-Forwarded-Host $http_host; } } }')
  if run(['docker','network','create',network]).returncode:raise RuntimeError('test network creation')
  created_network=True
  pgenv=os.environ.copy();pgenv.update(POSTGRES_USER='source_admin',POSTGRES_PASSWORD='synthetic-admin-password',POSTGRES_DB='synthetic_orders')
  r=run(['docker','run','-d','--name',pg,'--network',network,'-p','127.0.0.1::5432','-e','POSTGRES_USER','-e','POSTGRES_PASSWORD','-e','POSTGRES_DB','postgres@sha256:266d3c257594e195b309ae9d1805e54c788f1d7b375f56bf6c367aac6c917e7b'],env=pgenv)
  if r.returncode:raise RuntimeError('test PostgreSQL start')
  created.append(pg)
  for attempt in range(40):
   ready=run(['docker','exec',pg,'pg_isready','-U','source_admin','-d','synthetic_orders'])
   if ready.returncode==0:break
   time.sleep(1)
  else:raise RuntimeError('test PostgreSQL startup timeout')
  sql="CREATE ROLE demo_reader LOGIN PASSWORD 'synthetic-password'; CREATE TABLE public.orders(id integer PRIMARY KEY,status text NOT NULL); INSERT INTO public.orders VALUES(1,'paid'),(2,'pending'); GRANT CONNECT ON DATABASE synthetic_orders TO demo_reader; GRANT USAGE ON SCHEMA public TO demo_reader; GRANT SELECT ON public.orders TO demo_reader;"
  ready=run(['docker','exec','-i',pg,'psql','-v','ON_ERROR_STOP=1','-U','source_admin','-d','synthetic_orders'],input=sql)
  if ready.returncode:raise RuntimeError('test PostgreSQL seed')
  mapped=run(['docker','port',pg,'5432/tcp'])
  if mapped.returncode:raise RuntimeError('test PostgreSQL port')
  pgport=int(mapped.stdout.strip().rsplit(':',1)[1])
  r=run(['docker','run','-d','--name',smtp,'--network',network,'--network-alias','smtp','-v',td+':/work','--entrypoint','python3','python@sha256:a6e34c598f2467ed0e9a8d349809fcd8b5c603269512df273a0bb1784edc11b1','/work/smtp.py'])
  if r.returncode:raise RuntimeError('fake SMTP start')
  created.append(smtp)
  cmd=['docker','run','-d','--name',kc,'--network',network,'--network-alias','identity','-e','KC_BOOTSTRAP_ADMIN_USERNAME','-e','KC_BOOTSTRAP_ADMIN_PASSWORD','-e','KC_PROXY_HEADERS','-e','KC_HOSTNAME_STRICT','quay.io/keycloak/keycloak@sha256:357829ec7c4693397533035092ad13b0644bcc95ded311f33a3738c4d9e9bdba','start-dev']
  if run(cmd,env=env).returncode:raise RuntimeError('identity container start')
  created.append(kc)
  r=run(['docker','run','-d','--name',proxy,'--network',network,'-p','127.0.0.1::443','-v',td+':/work:ro','-v',str(spool/'nginx.conf')+':/etc/nginx/nginx.conf:ro','nginx@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10'])
  if r.returncode:raise RuntimeError('test TLS proxy')
  created.append(proxy)
  r=run(['docker','port',proxy,'443/tcp'])
  if r.returncode:raise RuntimeError('identity port')
  port=int(r.stdout.strip().rsplit(':',1)[1]);base='https://127.0.0.1:'+str(port)
  session=requests.Session();session.trust_env=False;session.verify=str(cert)
  for attempt in range(65):
   try:
    r=session.get(base+'/realms/master/.well-known/openid-configuration',timeout=2)
    if r.status_code==200:break
   except requests.RequestException:pass
   time.sleep(1)
  else:raise RuntimeError('identity startup timeout')
  r=session.post(base+'/realms/master/protocol/openid-connect/token',data={'grant_type':'password','client_id':'admin-cli','username':admin,'password':password},timeout=10)
  if r.status_code!=200:raise RuntimeError('bootstrap admin '+str(r.status_code))
  headers={'Authorization':'Bearer '+r.json()['access_token']}
  realm={'realm':'df-selfservice-test','enabled':True,'registrationAllowed':True,'verifyEmail':True,'resetPasswordAllowed':True,'sslRequired':'none','smtpServer':{'host':'smtp','port':'1025','from':'no-reply@example.invalid','ssl':'false','starttls':'false','auth':'false'}}
  r=session.post(base+'/admin/realms',json=realm,headers=headers,timeout=10)
  if r.status_code!=201:raise RuntimeError('realm setup '+str(r.status_code))
  client={'clientId':'trial-client','enabled':True,'publicClient':True,'standardFlowEnabled':True,'redirectUris':['http://127.0.0.1/callback'],'attributes':{'pkce.code.challenge.method':'S256'}}
  r=session.post(base+'/admin/realms/df-selfservice-test/clients',json=client,headers=headers,timeout=10)
  if r.status_code!=201:raise RuntimeError('client setup '+str(r.status_code))
  clientsurl=base+'/admin/realms/df-selfservice-test/clients'
  found=session.get(clientsurl,params={'clientId':'trial-client'},headers=headers,timeout=10);found.raise_for_status()
  clientid=found.json()[0]['id']
  mapper={'name':'data-fabric-audience','protocol':'openid-connect','protocolMapper':'oidc-audience-mapper','consentRequired':False,'config':{'included.custom.audience':'data-fabric-mcp','access.token.claim':'true','id.token.claim':'false'}}
  r=session.post(clientsurl+'/'+clientid+'/protocol-mappers/models',json=mapper,headers=headers,timeout=10)
  if r.status_code!=201:raise RuntimeError('audience mapper setup '+str(r.status_code))
  import hashlib,base64
  verifier=base64.urlsafe_b64encode(os.urandom(32)).rstrip(b'=').decode()
  challenge=base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b'=').decode()
  browser=requests.Session();browser.trust_env=False;browser.verify=str(cert)
  auth=base+'/realms/df-selfservice-test/protocol/openid-connect/auth'
  params={'client_id':'trial-client','response_type':'code','scope':'openid','redirect_uri':'http://127.0.0.1/callback','state':'test-state','code_challenge':challenge,'code_challenge_method':'S256'}
  r=browser.get(auth,params=params,timeout=15)
  r.raise_for_status();p=Parser();p.feed(r.text)
  links=[x for x in p.links if '/registration' in urllib.parse.urlsplit(x).path]
  if not links: print('Login link paths:',[urllib.parse.urlsplit(x).path for x in p.links])
  if not links:raise RuntimeError('registration link missing')
  r=browser.get(urllib.parse.urljoin(base,links[0]),timeout=15)
  if r.status_code!=200:
   snippets=re.findall(r'<(?:h1|p|title)[^>]*>(.*?)</(?:h1|p|title)>',r.text,re.S|re.I)
   print('Registration error text:',[re.sub(r'<[^>]+>',' ',x).strip()[:160] for x in snippets][:5])
   raise RuntimeError('registration page '+str(r.status_code))
  p=Parser();p.feed(r.text);action=p.forms.get('kc-register-form')
  if not action:raise RuntimeError('registration form missing')
  r=browser.post(urllib.parse.urljoin(base,action),data={'firstName':'Trial','lastName':'User','email':'trial@example.invalid','username':'trial-user','password':'TestingPassword123!','password-confirm':'TestingPassword123!'},timeout=15)
  if r.status_code!=200:raise RuntimeError('registration response '+str(r.status_code))
  for _ in range(25):
   if (spool/'message.eml').is_file():break
   time.sleep(1)
  else:raise RuntimeError('verification email missing')
  userurl=base+'/admin/realms/df-selfservice-test/users'
  r=session.get(userurl,headers=headers,params={'username':'trial-user','exact':'true'},timeout=10);r.raise_for_status()
  users=r.json()
  if len(users)!=1 or users[0].get('emailVerified') is not False:raise RuntimeError('unverified user state')
  msg=email.message_from_bytes((spool/'message.eml').read_bytes())
  chunks=[]
  for part in msg.walk():
   if part.get_content_type() in ('text/plain','text/html'):
    payload=part.get_payload(decode=True)
    if payload:chunks.append(payload.decode('utf-8','replace'))
  txt=html.unescape('\n'.join(chunks))
  links=re.findall(r'https?://[^\s<>"\']+',txt)
  candidates=[x for x in links if '/login-actions/action-token?' in x]
  if not candidates:raise RuntimeError('verification action link missing')
  link=candidates[0].rstrip('.,)')
  if urllib.parse.urlsplit(link).netloc!='127.0.0.1:'+str(port):raise RuntimeError('unexpected verification host')
  r=browser.get(link,timeout=15,allow_redirects=False)
  if r.status_code not in (200,302,303):raise RuntimeError('verification action '+str(r.status_code))
  r=session.get(userurl+'/'+users[0]['id'],headers=headers,timeout=10);r.raise_for_status()
  if r.json().get('emailVerified') is not True:raise RuntimeError('verification not persisted')
  print('PASS: isolated registration, fake email and verification completed')
  login=browser.get(auth,params=params,timeout=15,allow_redirects=False)
  if login.status_code==200:
   f=Parser();f.feed(login.text);login_action=f.forms.get('kc-form-login')
   if not login_action:raise RuntimeError('login form unavailable after verification')
   login=browser.post(urllib.parse.urljoin(base,login_action),data={'username':'trial-user','password':'TestingPassword123!'},timeout=15,allow_redirects=False)
  location=login.headers.get('Location','')
  parsed=urllib.parse.urlsplit(location)
  values=urllib.parse.parse_qs(parsed.query)
  if login.status_code not in (302,303) or parsed.scheme!='http' or parsed.netloc!='127.0.0.1' or parsed.path!='/callback' or values.get('state')!=['test-state'] or len(values.get('code',[]))!=1:
   raise RuntimeError('verified OAuth callback missing')
  token=session.post(base+'/realms/df-selfservice-test/protocol/openid-connect/token',data={'grant_type':'authorization_code','client_id':'trial-client','code':values['code'][0],'redirect_uri':'http://127.0.0.1/callback','code_verifier':verifier},timeout=15)
  if token.status_code!=200:raise RuntimeError('verified token exchange '+str(token.status_code))
  tokenfile=spool/'token.txt';tokenfile.write_text(token.json()['access_token']);tokenfile.chmod(0o600)
  second={'username':'other-user','firstName':'Other','lastName':'User','enabled':True,'email':'other@example.invalid','emailVerified':True,'credentials':[{'type':'password','value':'OtherPassword123!','temporary':False}]}
  r=session.post(userurl,json=second,headers=headers,timeout=10)
  if r.status_code!=201:raise RuntimeError('second synthetic user setup '+str(r.status_code))
  verifier2=base64.urlsafe_b64encode(os.urandom(32)).rstrip(b'=').decode()
  challenge2=base64.urlsafe_b64encode(hashlib.sha256(verifier2.encode()).digest()).rstrip(b'=').decode()
  browser2=requests.Session();browser2.trust_env=False;browser2.verify=str(cert)
  params2=dict(params,state='second-state',code_challenge=challenge2)
  login2=browser2.get(auth,params=params2,timeout=15)
  f=Parser();f.feed(login2.text);login_action=f.forms.get('kc-form-login')
  if not login_action:raise RuntimeError('second login form missing')
  login2=browser2.post(urllib.parse.urljoin(base,login_action),data={'username':'other-user','password':'OtherPassword123!'},timeout=15,allow_redirects=False)
  parsed2=urllib.parse.urlsplit(login2.headers.get('Location',''));values2=urllib.parse.parse_qs(parsed2.query)
  if login2.status_code not in (302,303) or parsed2.scheme!='http' or parsed2.netloc!='127.0.0.1' or parsed2.path!='/callback' or values2.get('state')!=['second-state'] or len(values2.get('code',[]))!=1:raise RuntimeError('second OAuth callback missing; status='+str(login2.status_code)+' destination='+parsed2.path+' text='+re.sub(r'<[^>]+>',' ',login2.text)[-300:])
  token2=session.post(base+'/realms/df-selfservice-test/protocol/openid-connect/token',data={'grant_type':'authorization_code','client_id':'trial-client','code':values2['code'][0],'redirect_uri':'http://127.0.0.1/callback','code_verifier':verifier2},timeout=15)
  if token2.status_code!=200:raise RuntimeError('second token exchange '+str(token2.status_code))
  tokenfile2=spool/'token2.txt';tokenfile2.write_text(token2.json()['access_token']);tokenfile2.chmod(0o600)
  testenv=os.environ.copy()
  testenv.update(DF_SELF_SERVICE_TEST_ISSUER=base+'/realms/df-selfservice-test',DF_SELF_SERVICE_TEST_CA=str(cert),DF_SELF_SERVICE_TEST_TOKEN=str(tokenfile),DF_SELF_SERVICE_TEST_OTHER_TOKEN=str(tokenfile2),DF_SELF_SERVICE_TEST_SOURCE_PORT=str(pgport))
  test=run([shutil.which('go') or 'go','test','./cmd/server','-run','^TestSelfServiceRealKeycloakMCPInitialize$','-count=1'],cwd=str(pathlib.Path(__file__).resolve().parents[1]),env=testenv)
  if test.returncode:
   print('Go integration test output:',test.stdout[-1500:].replace(token.json()['access_token'],'[redacted]').replace(token2.json()['access_token'],'[redacted]'))
   raise RuntimeError('Keycloak-to-MCP integration failed')
  print('PASS: two verified Keycloak identities initialized MCP; sample query and cross-tenant isolation passed')
except Exception as exc:
 print('Identity integration failed:',str(exc),file=sys.stderr)
 sys.exit(1)
finally:
 for name in reversed(created):
  r=run(['docker','rm','-f',name])
  print('Removed isolated test container:',r.returncode==0)
 if created_network:
  r=run(['docker','network','rm',network])
  print('Removed isolated test network:',r.returncode==0)
