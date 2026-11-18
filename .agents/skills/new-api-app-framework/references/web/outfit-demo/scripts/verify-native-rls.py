"""Run against an isolated Supabase test project with migrations applied.
Creates and deletes disposable users; never run against a production project.
SUPABASE_URL, SUPABASE_ANON_KEY and SUPABASE_SERVICE_ROLE_KEY are required.
"""
import os,json,urllib.request,urllib.error,base64,uuid
base=os.environ['SUPABASE_URL'].rstrip('/')
service=os.environ['SUPABASE_SERVICE_ROLE_KEY']; anon=os.environ['SUPABASE_ANON_KEY']
def request(path,token=service,body=None,method=None):
 data=None if body is None else json.dumps(body).encode()
 headers={'apikey':anon,'Authorization':'Bearer '+token,'Content-Type':'application/json','Prefer':'return=representation'}
 req=urllib.request.Request(base+path,data=data,headers=headers,method=method or ('GET' if body is None else 'POST'))
 try:
  with urllib.request.urlopen(req,timeout=15) as r:
   raw=r.read();return r.status,json.loads(raw) if raw else None
 except urllib.error.HTTPError as e:
  raw=e.read();return e.code,json.loads(raw) if raw else None
users=[]
for i in range(2):
 email='app-products-'+uuid.uuid4().hex+'@example.com'
 status,user=request('/auth/v1/admin/users',body={'email':email,'password':'Native-Test!2026','email_confirm':True})
 assert status==200,(status,user)
 status,session=request('/auth/v1/token?grant_type=password',anon,{'email':email,'password':'Native-Test!2026'})
 assert status==200,(status,session)
 claims=json.loads(base64.urlsafe_b64decode(session['access_token'].split('.')[1]+'=='))
 users.append((user['id'],session['access_token'],claims['session_id']))
 uid,token,sid=users[-1]
 payload={'user_id':uid,'issuer':base+'/auth/v1','app_id':i+7,'client_id':'proxy:'+str(i+7),'identity_source':'proxy','gateway_user_id':int(uuid.uuid4().hex[:10],16),'token_id':5+i,'encrypted_key':'test-ciphertext'}
 status,result=request('/rest/v1/gateway_proxy_accounts',body=payload)
 assert status==201,(status,result)
 status,result=request('/rest/v1/gateway_proxy_sessions',body={'session_id':sid,'user_id':uid,'issuer':base+'/auth/v1','app_id':i+7})
 assert status==201,(status,result)
 status,result=request('/rest/v1/rpc/gateway_proxy_session_active',body={'p_session_id':sid,'p_user_id':uid})
 assert status==200 and result is True,(status,result)
 status,result=request('/rest/v1/rpc/current_app_client_id',token,{})
 assert status==200 and result=='proxy:'+str(i+7),(status,result)
 for table in ['gateway_proxy_accounts','gateway_proxy_sessions','gateway_checkout_requests']:
  for credential in [anon,token]:
   status,result=request('/rest/v1/'+table,credential)
   assert status in [401,403],(table,status,result)
 status,result=request('/rest/v1/rpc/bootstrap_proxy_owner',token,{'p_user_id':uid,'p_client_id':'proxy:'+str(i+7)})
 assert status in [401,403,404],(status,result)
 status,result=request('/rest/v1/rpc/bootstrap_proxy_owner',body={'p_user_id':uid,'p_client_id':'proxy:'+str(i+7)})
 assert status==204,(status,result)
 status,result=request('/rest/v1/rpc/is_app_owner',token,{})
 assert status==200 and result is True,(status,result)
uid,token,sid=users[0]
status,result=request('/rest/v1/rpc/gateway_proxy_session_active',body={'p_session_id':users[1][2],'p_user_id':uid})
assert status==200 and result is False
status,result=request('/auth/v1/logout?scope=local',token,{})
assert status==204,(status,result)
status,result=request('/rest/v1/rpc/gateway_proxy_session_active',body={'p_session_id':sid,'p_user_id':uid})
assert status==200 and result is False,(status,result)
status,result=request('/rest/v1/rpc/current_app_client_id',token,{})
assert status==200 and not result,(status,result)
print('PASS: native password login; verified session ownership; private table grants/RLS; native owner bootstrap; cross-session denial; immediate logout revocation')
for uid,_,_ in users:
 status,result=request('/auth/v1/admin/users/'+uid,method='DELETE')
 assert status==200,(status,result)

