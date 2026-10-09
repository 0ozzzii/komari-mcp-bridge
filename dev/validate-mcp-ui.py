from pathlib import Path
import tempfile,os,subprocess,secrets,time,socket,shutil,json
from playwright.sync_api import sync_playwright
import urllib.request,urllib.error,http.cookiejar
class Reply:
    def __init__(self,r):self.status_code=r.code;self.data=r.read()
    def json(self):return json.loads(self.data)
class HTTP:
    def __init__(self):
        self.cookies=http.cookiejar.CookieJar()
        self.opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cookies))
    def fetch(self,method,url,payload=None,timeout=5):
        data=None if payload is None else json.dumps(payload).encode()
        req=urllib.request.Request(url,data=data,method=method,headers={'Accept':'application/json, text/event-stream'} if data is None else {'Content-Type':'application/json','Accept':'application/json, text/event-stream'})
        try:
            with self.opener.open(req,timeout=timeout) as r:return Reply(r)
        except urllib.error.HTTPError as r:return Reply(r)
    def get(self,url,timeout=5):return self.fetch('GET',url,timeout=timeout)
    def post(self,url,json=None,timeout=5):return self.fetch('POST',url,json,timeout)
requests=HTTP()
requests.Session=HTTP
requests.RequestException=urllib.error.URLError
import argparse
parser=argparse.ArgumentParser(description='Isolated panel/bridge Chromium UI validation; never connects to production')
parser.add_argument('--panel',required=True,help='Path to the built native panel executable')
parser.add_argument('--bridge',help='Path to the built bridge executable')
parser.add_argument('--chromium',default='/usr/bin/chromium')
parser.add_argument('--screenshot',help='Optional screenshot path (test fixtures only)')
options=parser.parse_args()
repo=Path(__file__).resolve().parents[1]
run=Path(tempfile.mkdtemp(prefix='komari-menu-ui-',dir='/tmp'))
procs=[];logs=[]
def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1',0));return s.getsockname()[1]
panel_port,mcp_port,control_port=port(),port(),port()
base=f'http://127.0.0.1:{panel_port}'
control=secrets.token_urlsafe(32);key=secrets.token_urlsafe(32);password='Local9A'+secrets.token_urlsafe(24)
def start(binary,args,env,name):
    f=(run/(name+'.log')).open('wb');logs.append(f)
    p=subprocess.Popen([binary]+args,cwd=run,env=os.environ|env,stdout=f,stderr=subprocess.STDOUT);procs.append(p);return p
def await_get(url,status=200,limit=30):
    end=time.monotonic()+limit
    while time.monotonic()<end:
        try:
            r=requests.get(url,timeout=1)
            if r.status_code==status:return r
        except requests.RequestException:pass
        time.sleep(.1)
    raise RuntimeError('local readiness check timed out: '+url)
try:
    start(str(Path(options.panel).resolve()),['server','--listen',f'127.0.0.1:{panel_port}','--database',str(run/'panel.db')],{'KOMARI_MCP_CONTROL_URL':f'http://127.0.0.1:{control_port}','KOMARI_MCP_CONTROL_TOKEN':control,'KOMARI_MCP_UPSTREAM_URL':f'http://127.0.0.1:{mcp_port}/mcp'},'panel')
    await_get(base+'/api/install/status')
    r=requests.post(base+'/api/install/complete',json={'username':'admin','password':password,'sitename':'Komari 本地隔离验证','description':'原后台一级 MCP 菜单验证','metric_dsn':f'file:{run}/metrics.db'},timeout=20)
    assert r.status_code==200,('installation',r.status_code)
    await_get(base+'/api/admin/mcp',status=401)
    session=requests.Session()
    r=session.post(base+'/api/login',json={'username':'admin','password':password},timeout=10)
    assert r.status_code==200,('login',r.status_code)
    r=session.post(base+'/api/rpc2',json={'jsonrpc':'2.0','id':1,'method':'admin:editSettings','params':{'api_key':key,'eula_accepted':True}},timeout=10)
    assert r.status_code==200 and 'error' not in r.json(),('settings',r.status_code)
    r=session.post(base+'/api/admin/theme/settings?theme=default',json={'_komari_onboarding_v1':{'seen':['install','workbench','notifications','markets','terminal'],'workbenchOpened':True}},timeout=10)
    assert r.status_code==200,('guide',r.status_code)
    # Closed fixture: bounded saved output only; no terminal connects to any probe.
    from datetime import datetime,timezone
    fixture_time=datetime.now(timezone.utc).isoformat().replace('+00:00','Z')
    fixture_dir=run/'bridge-state'/'sessions';fixture_dir.mkdir(parents=True)
    fixture_prefix='\x1b[2J\x1b[1;1H'+' '*80+'\r\n'*20
    fixture_text='中文回显：仅本地模拟\n'*1000
    fixture_output=(fixture_prefix+fixture_text).encode()
    (fixture_dir/'ui-log-session.log').write_bytes(fixture_output)
    (fixture_dir/'ui-log-session.json').write_text(json.dumps({'session_id':'ui-log-session','owner':'fixture-owner','node_uuid':'fixture-node','connection_state':'closed','generation':1,'created_at':fixture_time,'last_tool_call':fixture_time,'commands':{},'output_end':len(fixture_output),'output_log_truncated':True}))
    start(str(Path(options.bridge).resolve()) if options.bridge else str(repo/'bridge/bin/komari-mcp'),['serve'],{'KOMARI_BASE_URL':base,'KOMARI_API_KEY':key,'BRIDGE_CONTROL_TOKEN':control,'BRIDGE_BIND_ADDRESS':f'127.0.0.1:{mcp_port}','BRIDGE_CONTROL_ADDRESS':f'127.0.0.1:{control_port}','BRIDGE_STATE_DIR':str(run/'bridge-state')},'bridge')
    await_get(f'http://127.0.0.1:{mcp_port}/healthz')
    def add_node(name):
        r=session.post(base+'/api/rpc2',json={'jsonrpc':'2.0','id':2,'method':'admin:addClient','params':{'name':name}})
        assert r.status_code==200 and 'error' not in r.json()
        value=r.json()['result'];tokens[value['uuid']]=value['token'];return value['uuid']
    tokens={}
    ids=[add_node('隔离节点 A'),add_node('隔离节点 B')]
    for uuid,weight,region in [(ids[0],9,'KR'),(ids[1],-1,'🇺🇸')]:
        r=session.post(base+'/api/rpc2',json={'jsonrpc':'2.0','id':3,'method':'admin:editClient','params':{'uuid':uuid,'weight':weight,'region':region}})
        assert r.status_code==200 and 'error' not in r.json()
    report=requests.post(base+'/api/clients/v2/rpc',json={'jsonrpc':'2.0','id':4,'method':'agent.report','params':{'report':{}},'token':tokens[ids[1]]})
    assert report.status_code==200, ('isolated simulated report',report.status_code)
    listing=session.get(base+'/api/admin/mcp/control/nodes',timeout=5).json()
    assert [n['uuid'] for n in listing]==[ids[1],ids[0]]
    assert listing[0]['online'] and not listing[1]['online'] and listing[0]['last_seen']
    assert all('token' not in n for n in listing)

    def mcp(url,method,params):
        r=requests.post(url,json={'jsonrpc':'2.0','id':8,'method':method,'params':params})
        assert r.status_code==200,('MCP status',r.status_code)
        # Official SDK defaults to SSE; no Bearer or administrator cookie used.
        raw=r.data.decode()
        value=json.loads(next(line[6:] for line in raw.splitlines() if line.startswith('data: '))) if raw.startswith('event:') or raw.startswith('data:') else json.loads(raw)
        assert 'error' not in value
        return value['result']
    def listed(url):
        result=mcp(url,'tools/call',{'name':'komari_nodes_list','arguments':{}})
        assert not result.get('isError'), 'nodes tool failed'
        return result['structuredContent']

    with sync_playwright() as pw:
        browser=pw.chromium.launch(executable_path=options.chromium,headless=True,args=['--no-sandbox'])
        ctx=browser.new_context(viewport={'width':1440,'height':1024},locale='zh-CN')
        ctx.add_init_script("localStorage.setItem('language','zh-CN')")
        ctx.add_cookies([{'name':c.name,'value':c.value,'url':base} for c in session.cookies])
        release_requests=[]
        def browser_route(route):
            url=route.request.url
            if 'api.github.com/repos/' in url:
                release_requests.append(url)
                if '/0ozzzii/komari-mcp-bridge/releases' in url:
                    assets=[{'name':n,'size':1} for n in ['release.json','sha256sums.txt','komari-linux-amd64']]
                    releases=[{'tag_name':'v9.0.0','draft':False,'prerelease':False,'assets':[],'html_url':'https://github.com/0ozzzii/komari-mcp-bridge/releases/tag/v9.0.0'}, {'tag_name':'v1.0.4','draft':False,'prerelease':False,'assets':assets,'html_url':'https://github.com/0ozzzii/komari-mcp-bridge/releases/tag/v1.0.4'}]
                    route.fulfill(json=releases);return
            route.continue_() if url.startswith(base) else route.abort()
        ctx.route('**/*',browser_route)
        page=ctx.new_page();errors=[]
        page.on('pageerror',lambda e:errors.append(str(e)))
        page.goto(base+'/admin/mcp',wait_until='networkidle')
        menu=page.get_by_role('link',name='MCP',exact=True)
        menu.wait_for(state='visible')
        account=page.locator('[data-onboarding-nav="/admin/account"]')
        # Check the actual sidebar link's preceding text in visual order.
        frame=page.frame_locator('iframe[title="MCP"]')
        frame.get_by_role('heading',name='MCP 工具接入',exact=True).wait_for(state='visible')
        frame.get_by_text('MCP 已停用',exact=True).wait_for(state='visible')
        frame.get_by_text('尚未创建密钥。',exact=True).wait_for(state='visible')
        frame.get_by_role('button',name='启用 MCP',exact=True).click()
        frame.get_by_text('MCP 已启用',exact=True).wait_for(state='visible')
        frame.locator('#name').fill('本地权限验证')
        creator=frame.locator('#nodes')
        assert creator.locator('.node-row input').evaluate_all('(els)=>els.map(e=>e.value)')==[ids[1],ids[0]]
        assert not any(uuid in creator.inner_text() for uuid in ids)
        assert creator.locator('.node-flag').evaluate_all('(els)=>els.map(e=>e.getAttribute("src"))')==['/assets/flags/US.svg','/assets/flags/KR.svg']
        creator.get_by_role('combobox',name='节点状态筛选').select_option('online')
        assert creator.locator('.node-row:visible').count()==1
        creator.get_by_role('button',name='全选当前节点',exact=True).click()
        assert creator.locator('input:checked').count()==1
        creator.get_by_role('combobox',name='节点状态筛选').select_option('offline')
        assert creator.locator('.node-row:visible').count()==1
        assert creator.locator('input:checked').count()==1
        creator.get_by_role('button',name='全选当前节点',exact=True).click()
        assert creator.locator('input:checked').count()==1
        assert creator.locator('input:checked').input_value()==ids[0], 'filtered select-all retained hidden online node'
        creator.get_by_role('combobox',name='节点状态筛选').select_option('all')
        creator.get_by_role('button',name='全选当前节点',exact=True).click()
        assert creator.locator('input:checked').count()==2
        creator.get_by_role('button',name='清空选择',exact=True).click()
        assert creator.locator('input:checked').count()==0
        assert frame.locator('#create').is_disabled()
        creator.locator('input').first.check()
        assert creator.locator('input:checked').count()==1
        creator.get_by_role('button',name='全选当前节点',exact=True).click()
        frame.locator('#create').click()
        frame.locator('#secret').wait_for(state='visible')
        url=frame.locator('#connection-url').input_value()
        assert url.startswith(base+'/mcp/kmb_')
        mcp(url,'initialize',{'protocolVersion':'2025-11-25','capabilities':{},'clientInfo':{'name':'local-url-test','version':'1'}})
        tools=mcp(url,'tools/list',{})['tools']
        assert len(tools)==15
        toolmap={t['name']:t for t in tools}
        for name in ['komari_command_run','komari_terminal_input','komari_filesystem']:
            annotations=toolmap[name]['annotations']
            assert annotations['readOnlyHint'] is False and annotations['destructiveHint'] is True
            assert annotations['idempotentHint'] is False and annotations['openWorldHint'] is True
        assert 'komari_execution_policy' in toolmap['komari_session_open']['description']
        capabilities=mcp(url,'tools/call',{'name':'komari_capabilities','arguments':{}})['structuredContent']
        assert capabilities['bridge_version'] and capabilities['bridge_commit']
        assert {n['uuid'] for n in listed(url)}==set(ids)
        add_node('后续新增隔离节点')
        assert {n['uuid'] for n in listed(url)}==set(ids), 'all selection unexpectedly granted future node'
        frame.get_by_role('button',name='已保存，关闭',exact=True).click()
        frame.locator('#secret').wait_for(state='hidden')
        assert frame.locator('#connection-url').input_value()==''
        row=frame.locator('#keys .key')
        assert not row.evaluate('(e)=>e.open'), 'new key was not collapsed'
        row.locator('summary').first.click()
        row.locator('.scope input').nth(1).uncheck()
        row.get_by_role('button',name='保存授权',exact=True).click()
        frame.get_by_text('节点与工具授权已更新。',exact=True).wait_for(state='visible')
        assert [n['uuid'] for n in listed(url)]==[ids[1]], 'existing URL scope did not update'
        row.get_by_role('button',name='停用此密钥',exact=True).click()
        frame.get_by_text('密钥状态已更新。',exact=True).wait_for(state='visible')
        assert requests.post(url,json={'jsonrpc':'2.0','id':9,'method':'tools/list','params':{}}).status_code==401
        # All administrator controls remain within the native MCP page.
        frame.locator('#execution-policy').click()
        policy=frame.locator('#policy-dialog')
        policy.wait_for(state='visible')
        box=policy.bounding_box()
        assert box['y']>=16 and box['y']+box['height']<=1008, ('policy clipped to viewport',box)
        assert frame.locator('#execution-policy').evaluate('(e)=>!!e.closest(".card")')
        assert frame.locator('#logs-open').evaluate('(e)=>!!e.closest(".card")')
        page.mouse.click(box['x']-8,box['y']+8)
        policy.wait_for(state='hidden')
        frame.locator('#execution-policy').click()
        policy.wait_for(state='visible')
        page.keyboard.press('Escape')
        policy.wait_for(state='hidden')
        frame.locator('#execution-policy').click()
        policy.wait_for(state='visible')
        assert policy.locator('#p-parallel').input_value()=='0'
        assert policy.locator('#p-lease').input_value()=='10'
        assert policy.locator('#p-default-timeout').input_value()=='30'
        assert policy.locator('#p-max-timeout').input_value()=='360'
        policy.locator('#p-default-timeout').fill('361')
        policy.locator('#policy-save').click()
        policy.locator('#policy-error').get_by_text('invalid execution policy bounds',exact=True).wait_for(state='visible')
        policy.locator('#p-default-timeout').fill('30')
        policy.locator('#policy-scope').select_option('node')
        policy.locator('#policy-target').select_option(ids[0])
        policy.locator('#p-parallel').fill('2')
        policy.locator('#p-maintenance').check()
        policy.locator('#policy-save').click()
        policy.locator('#policy-error').get_by_text('已保存',exact=False).wait_for(state='visible')
        policy.locator('#policy-close').click()
        page.reload(wait_until='networkidle')
        frame=page.frame_locator('iframe[title="MCP"]')
        frame.locator('#execution-policy').click()
        policy=frame.locator('#policy-dialog')
        policy.locator('#policy-scope').select_option('node')
        policy.locator('#policy-target').select_option(ids[0])
        assert policy.locator('#p-parallel').input_value()=='2', 'device policy did not persist'
        assert policy.locator('#p-maintenance').is_checked()
        policy.locator('#policy-scope').select_option('key')
        policy.locator('#p-parallel').fill('1')
        policy.locator('#p-max-timeout').fill('120')
        policy.locator('#policy-save').click()
        policy.locator('#policy-error').get_by_text('已保存',exact=False).wait_for(state='visible')
        policy.locator('#policy-scope').select_option('node')
        policy.locator('#policy-scope').select_option('key')
        assert policy.locator('#p-parallel').input_value()=='1'
        assert policy.locator('#p-max-timeout').input_value()=='120'
        policy.locator('#policy-reset').click()
        policy.locator('#policy-error').get_by_text('已保存',exact=False).wait_for(state='visible')
        assert policy.locator('#p-parallel').input_value()=='0'
        policy.locator('#policy-close').click()
        # Local simulated audit fixtures exercise the actual authenticated HTTP reader and JS modal.
        audit=run/'bridge-state'/'audit.jsonl'
        with audit.open('a') as f:
            for index in range(125):
                f.write(json.dumps({'time':datetime.now(timezone.utc).isoformat().replace('+00:00','Z'),'event':'command_state','node_uuid':ids[index%2],'command_id':'ui-'+str(index),'session_id':'ui-log-session','execution_state':'completed','exit_code':3,'output_gap':True,'command':'PRIVATE-COMMAND','Authorization':'PRIVATE-AUTH','continuity_nonce':'PRIVATE-NONCE'})+'\n')
            f.write(json.dumps({'time':datetime.now(timezone.utc).isoformat().replace('+00:00','Z'),'event':'tool','node_uuid':ids[0],'tool':'<img src=x onerror="window.logXSS=1">','tool_success':False})+'\n')
        assert requests.get(base+'/api/admin/mcp/log-viewer.js').status_code==401
        assert requests.get(base+'/api/admin/mcp/control/logs').status_code==401
        assert requests.get(base+'/api/admin/mcp/control/logs/output?session_id=ui-log-session').status_code==401
        frame.locator('#logs-open').click()
        log_dialog=frame.locator('#logs-dialog');log_dialog.wait_for(state='visible')
        ordered=[n['uuid'] for n in session.get(base+'/api/admin/mcp/control/nodes').json()]
        assert log_dialog.locator('#logs-node option').evaluate_all('(els)=>els.slice(1).map(e=>e.value)')==ordered
        log_dialog.locator('#logs-node').select_option(ids[0])
        log_dialog.locator('#logs-type').select_option('command_state')
        log_dialog.locator('#logs-problem').select_option('problem')
        log_dialog.locator('#logs-time').select_option('0')
        log_dialog.locator('#logs-refresh').click()
        log_dialog.locator('#logs-status').get_by_text('已显示 50 条',exact=False).wait_for(state='visible')
        assert log_dialog.locator('.log-record').count()==50
        box=log_dialog.bounding_box()
        assert box['y']>=16 and box['y']+box['height']<=1008, ('loaded log dialog clipped',box)
        log_dialog.locator('#logs-more').click()
        log_dialog.locator('#logs-status').get_by_text('已显示 63 条',exact=False).wait_for(state='visible')
        assert log_dialog.locator('.log-record').count()==63
        assert log_dialog.locator('#logs-more').is_disabled()
        assert all('隔离节点 A' in label for label in log_dialog.locator('.log-record summary').all_text_contents())
        assert all(uuid not in ' '.join(log_dialog.locator('.log-record summary').all_text_contents()) for uuid in ids)
        item=log_dialog.locator('.log-record').first;item.locator('summary').click()
        assert not any(secret in item.inner_text() for secret in ['PRIVATE-COMMAND','PRIVATE-AUTH','PRIVATE-NONCE'])
        item.get_by_role('button',name='查看会话回显',exact=True).click()
        item.get_by_text('已读取',exact=False).wait_for(state='visible')
        assert '中文回显：仅本地模拟' in item.locator('.log-output pre').inner_text()
        assert '存在截断' in item.locator('.log-output').inner_text() and '输出缺口' in item.locator('.log-output').inner_text()
        item.get_by_role('button',name='继续读取回显',exact=True).click()
        item.get_by_text('已读取 '+str(len(fixture_output)),exact=False).wait_for(state='visible')
        assert item.locator('.log-output pre').inner_text()==fixture_text, 'display retains terminal startup blank lines'
        item.locator('.log-display-toggle input').check()
        assert item.locator('.log-output pre').text_content()==fixture_output.decode(), 'original output bytes not retained in display'
        item.locator('.log-display-toggle input').uncheck()
        log_dialog.locator('#logs-type').select_option('tool');log_dialog.locator('#logs-refresh').click()
        log_dialog.locator('#logs-status').get_by_text('已显示 1 条',exact=False).wait_for(state='visible')
        log_dialog.locator('.log-record summary').first.click()
        log_dialog.get_by_text('<img src=x onerror="window.logXSS=1">',exact=False).wait_for(state='visible')
        assert not log_dialog.locator('.log-record img[src="x"]').count()
        assert page.locator('iframe[title="MCP"]').evaluate('(f)=>!f.contentWindow.logXSS')
        log_dialog.locator('#logs-close').click()
        log_dialog.locator('.log-record').first.wait_for(state='detached')
        assert log_dialog.locator('.log-record').count()==0
        # Rapid close/reopen while a read is pending must schedule a fresh snapshot.
        frame.locator('#logs-open').click();log_dialog.locator('#logs-close').click();frame.locator('#logs-open').click()
        log_dialog.locator('.log-record').first.wait_for(state='visible')
        log_dialog.locator('#logs-close').click()
        # Credential must not enter panel access logs, including URL-only requests.
        assert url.rsplit('/',1)[1].encode() not in (run/'panel.log').read_bytes()
        frame.get_by_role('button',name='停用 MCP',exact=True).click()
        frame.get_by_text('MCP 已停用',exact=True).wait_for(state='visible')
        page.reload(wait_until='networkidle')
        page.frame_locator('iframe[title="MCP"]').get_by_text('MCP 已停用',exact=True).wait_for(state='visible')
        page.get_by_role('link',name='服务器列表',exact=True).click()
        page.wait_for_url('**/admin/servers')
        menu.click();page.wait_for_url('**/admin/mcp')
        page.frame_locator('iframe[title="MCP"]').get_by_role('heading',name='MCP 工具接入',exact=True).wait_for(state='visible')
        assert not errors,errors
        for name in ['另一个测试 Key','第三个测试 Key']:
            r=session.post(base+'/api/admin/mcp/control/keys/create',json={'name':name,'nodes':[ids[0]],'permissions':['terminal']})
            # CSRF header required on mutations; this deliberately lacks it.
            assert r.status_code==403
            req=urllib.request.Request(base+'/api/admin/mcp/control/keys/create',data=json.dumps({'name':name,'nodes':[ids[0]],'permissions':['terminal']}).encode(),method='POST',headers={'Content-Type':'application/json','X-Komari-MCP-Action':'1'})
            with session.opener.open(req) as response: assert response.status==200
        page.reload(wait_until='networkidle')
        frame=page.frame_locator('iframe[title="MCP"]')
        frame.locator('#keys .key').last.wait_for(state='visible')
        assert frame.locator('#keys .key').count()==3
        assert frame.locator('#keys .key[open]').count()==0
        frame.locator('#keys .key summary').first.click()
        assert frame.locator('#keys .key[open]').count()==1
        frame.locator('#keys .key summary').first.click()
        page.wait_for_timeout(400)
        assert page.locator('iframe[title="MCP"]').evaluate('(f)=>f.contentDocument.documentElement.scrollHeight<=f.clientHeight+2'), 'duplicate iframe page scrolling'
        assert frame.locator('.scope').first.evaluate('(e)=>getComputedStyle(e).scrollbarWidth')=='thin'
        assert frame.locator('.node-flag').first.evaluate('(e)=>e.naturalWidth>0'), 'flag assets not loaded'
        # Expire only simulated HTTP presence, not a production probe.
        time.sleep(36)
        page.reload(wait_until='networkidle')
        frame=page.frame_locator('iframe[title="MCP"]')
        frame.locator('#nodes .node-row').first.wait_for(state='visible')
        assert frame.locator('#nodes .node-state.online').count()==0
        assert '最后在线时间未知' not in frame.locator('#nodes .node-row').first.inner_text()
        assert '最后在线：' in frame.locator('#nodes .node-row').first.inner_text()
        page.set_viewport_size({'width':390,'height':844})
        page.wait_for_timeout(400)
        assert page.locator('iframe[title="MCP"]').evaluate('(f)=>f.contentDocument.documentElement.scrollHeight<=f.clientHeight+2'), 'mobile iframe scroll mismatch'
        assert page.locator('iframe[title="MCP"]').evaluate('(f)=>f.contentDocument.documentElement.scrollWidth<=f.clientWidth+2'), 'mobile horizontal overflow'
        frame.locator('#logs-open').click()
        log_dialog=frame.locator('#logs-dialog');log_dialog.wait_for(state='visible')
        box=log_dialog.bounding_box()
        assert box['x']>=0 and box['x']+box['width']<=392, ('mobile log dialog overflow',box)
        assert box['y']>=16 and box['y']+box['height']<=828, ('mobile log dialog clipped',box)
        log_dialog.get_by_role('button',name='关闭日志记录',exact=True).click()
        log_dialog.wait_for(state='hidden')
        page.set_viewport_size({'width':1440,'height':1024})
        page.wait_for_timeout(400)
        frame.locator('#name').fill('滚动后弹窗验证')
        frame.locator('#nodes').get_by_role('button',name='全选当前节点',exact=True).click()
        frame.locator('#create').click()
        frame.locator('#secret').wait_for(state='visible')
        rect=page.locator('iframe[title="MCP"]').bounding_box();dialog=frame.locator('#secret').bounding_box()
        assert dialog['y']>=0 and dialog['y']+dialog['height']<=1024, ('dialog outside outer viewport',dialog)
        frame.get_by_role('button',name='已保存，关闭',exact=True).click()
        assert not errors,errors
        assert release_requests and all('/0ozzzii/komari-mcp-bridge/releases' in url for url in release_requests), release_requests
        assert 'Komari MCP' in page.locator('header').inner_text() if page.locator('header').count() else 'Komari MCP' in page.inner_text('body')
        page.locator('.check-update').click()
        update=page.get_by_role('dialog')
        update.wait_for(state='visible')
        assert 'v1.0.4' in update.inner_text() and 'v9.0.0' not in update.inner_text(), 'source-only release advertised as installable'
        assert update.get_by_role('link',name='Komari MCP Releases').get_attribute('href')=='https://github.com/0ozzzii/komari-mcp-bridge/releases/tag/v1.0.4'
        page.keyboard.press('Escape')
        image=Path(options.screenshot).resolve() if options.screenshot else None
        if image:
            page.set_viewport_size({'width':1440,'height':1600})
            page.wait_for_timeout(400)
            page.screenshot(path=str(image),full_page=True)
        anon=browser.new_context(locale='zh-CN');anon.route('**/*',lambda route: route.continue_() if route.request.url.startswith(base) else route.abort())
        unauth=anon.new_page();unauth.goto(base+'/admin/mcp',wait_until='networkidle');unauth.wait_for_url('**/admin/login**')
        assert requests.get(base+'/api/admin/mcp',timeout=5).status_code==401
        browser.close()
    print(json.dumps({'scope':'actual Linux loopback panel, fresh SQLite/admin login, bridge and Chromium; node presence uses a locally simulated probe HTTP report; no production, Windows 测试节点 or remote probe','checks':['server-list weight ordering and stable selection','ISO and emoji flag assets','no UUID in node labels','online/offline filter','select filtered replaces all prior hidden selection','offline last report timestamp and unknown state','keys collapsed by default and expandable','desktop and mobile single page scrollbar','thin bounded list scrollbar','dialog visible after scrolling','all current offline nodes selectable','clear and individual selection','native creation dialog with complete credential URL','URL-only initialize and 15 tools','scoped node list','future nodes not automatically granted','existing URL reflects edited scope','key disable rejects same URL','dialog close clears plaintext','panel log does not contain URL key','native MCP sidebar visible','management frame loaded','toggle enable/disable persisted','direct route refresh','server list navigation and return','anonymous login redirect','anonymous management API rejected','MCP-only execution policy dialog','invalid policy rejected','device policy persistence','Key limits and reset inheritance','authenticated log modal with device/type/problem filtering','audit snapshot pagination','log field redaction and safe text rendering','lazy UTF-8 output pagination and truncation/gap notice','rapid reopen and mobile log modal','anonymous log and viewer API rejected','modal bounds after dynamic log loading, backdrop/Esc/top close', 'display cleanup and original text toggle preserve cursors', 'custom public release API and source-only release exclusion', 'explicit MCP safety annotations and bridge build metadata', 'no browser JS errors'],'image':str(image) if image else None},ensure_ascii=False))
finally:
    for p in reversed(procs):
        p.terminate()
        try:p.wait(timeout=12)
        except subprocess.TimeoutExpired:p.kill();p.wait()
    for f in logs:f.close()
    shutil.rmtree(run)
