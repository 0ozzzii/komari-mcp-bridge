#!/usr/bin/env python3
"""Chromium regression against the real management HTML with local API fixtures.

No panel credentials, real nodes, or external network are used. The parent fixture
covers one-axis overflow declarations and SPA-style iframe removal.
"""
import argparse
import json
from pathlib import Path
from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--chromium', default='/usr/bin/chromium')
parser.add_argument('--html', type=Path, default=ROOT / 'internal/mcpbridge/management.html')
args = parser.parse_args()
html = args.html.read_text()
logs = (ROOT / 'internal/mcpbridge/log-viewer.js').read_text()
policy = {'default': {'max_parallel': 0, 'lease_seconds': 600,
          'default_timeout_seconds': 1800, 'max_timeout_seconds': 21600,
          'idle_seconds': 1800, 'output_bytes': 1048576, 'min_free_percent': 5,
          'maintenance_seconds': 600}, 'nodes': {}, 'keys': {}, 'leases': []}
outer = '''<!doctype html><style>body{margin:0}#viewport{height:calc(100vh - 64px)}
iframe{border:0;width:100%;height:1800px}</style>
<button style="height:64px" onclick="document.querySelector('iframe').replaceWith(Object.assign(document.createElement('div'),{innerHTML:'<div style=height:1800px>另一管理页面</div>'}))">切换页面</button>
<main id="viewport" style="overflow-y:auto!important"><iframe title="MCP"
src="/api/admin/mcp" onload="this.contentDocument.documentElement.style.overflow='hidden'"></iframe></main>'''
with sync_playwright() as p:
    browser = p.chromium.launch(executable_path=args.chromium, headless=True, args=['--no-sandbox'])
    page = browser.new_page(viewport={'width': 1200, 'height': 800})
    errors = []
    page.on('pageerror', lambda error: errors.append(str(error)))
    def route(request):
        path = request.request.url.removeprefix('http://komari.test')
        if path == '/': request.fulfill(body=outer, content_type='text/html')
        elif path == '/api/admin/mcp': request.fulfill(body=html, content_type='text/html')
        elif path == '/api/admin/mcp/log-viewer.js': request.fulfill(body=logs, content_type='application/javascript')
        elif path == '/api/admin/mcp/control/status': request.fulfill(json={'enabled': True})
        elif path == '/api/admin/mcp/control/keys': request.fulfill(json={'keys': []})
        elif path == '/api/admin/mcp/control/nodes': request.fulfill(json=[])
        elif path == '/api/admin/mcp/control/policy': request.fulfill(json=policy)
        elif path.startswith('/api/admin/mcp/control/logs?'): request.fulfill(json={'entries': [], 'records': [], 'has_more': False})
        else: request.abort()
    page.route('**/*', route)
    def load():
        page.goto('http://komari.test/')
        frame = page.frame_locator('iframe')
        frame.get_by_text('尚未创建密钥。', exact=True).wait_for()
        return frame
    def overflow_state():
        return page.locator('#viewport').evaluate('''e => ['overflow-x','overflow-y'].map(name =>
            [e.style.getPropertyValue(name),e.style.getPropertyPriority(name),getComputedStyle(e).getPropertyValue(name)])''')
    def verify_scroll():
        viewport = page.locator('#viewport')
        viewport.evaluate('e=>e.scrollTop=0')
        page.mouse.move(1000, 450)
        page.mouse.wheel(0, 300)
        page.wait_for_function("document.querySelector('#viewport').scrollTop>0")
        viewport.evaluate('e=>e.scrollTop=0')
    checks = []
    # Opening from a scrolled page must keep the title and close button below
    # the admin header, not merely inside the whole browser viewport.
    frame = load()
    for dialog_id in ['#policy-dialog', '#logs-dialog']:
        positions = []
        for scroll in [0, 500]:
            page.locator('#viewport').evaluate('(e,y)=>e.scrollTop=y', scroll)
            page.locator('iframe').evaluate('''(iframe,id)=>
                iframe.contentWindow.eval("openDialog(document.getElementById("+JSON.stringify(id)+"))")''', dialog_id[1:])
            dialog = frame.locator(dialog_id)
            dialog.wait_for(state='visible')
            viewport_box = page.locator('#viewport').bounding_box()
            box = dialog.bounding_box()
            close_box = dialog.locator('.dialog-dismiss').bounding_box()
            assert box['y'] >= viewport_box['y']+15, (dialog_id, scroll, box, viewport_box)
            assert box['y']+box['height'] <= viewport_box['y']+viewport_box['height']-15
            assert close_box['y'] >= viewport_box['y']
            positions.append((box['y'], box['height']))
            dialog.locator('.dialog-dismiss').click()
            dialog.wait_for(state='hidden')
            page.wait_for_function("getComputedStyle(document.querySelector('#viewport')).overflowY==='auto'")
            assert page.locator('#viewport').evaluate('e=>e.scrollTop') == scroll
        assert all(abs(a-b)<2 for a,b in zip(*positions)), (dialog_id, positions)
    checks.append('both dialogs stay within admin viewport at top and after scrolling; background position preserved')
    for style in ['overflow-y:auto!important', 'overflow-x:hidden!important;overflow-y:auto']:
        frame = load()
        page.locator('#viewport').evaluate('(e,s)=>e.setAttribute("style",s)', style)
        before = overflow_state()
        verify_scroll()
        for opener, dialog_id, footer in [('#execution-policy', '#policy-dialog', '#policy-close'),
                                          ('#logs-open', '#logs-dialog', '#logs-close')]:
            for close in ['top', 'footer', 'escape', 'backdrop']:
                frame.locator(opener).click()
                dialog = frame.locator(dialog_id)
                dialog.wait_for(state='visible')
                assert page.locator('#viewport').evaluate('e=>getComputedStyle(e).overflowY') == 'hidden'
                if close == 'top': dialog.locator('.dialog-dismiss').click()
                elif close == 'footer': dialog.locator(footer).click()
                elif close == 'escape': page.keyboard.press('Escape')
                else:
                    box = dialog.bounding_box()
                    page.mouse.click(box['x'] - 8, box['y'] + 8)
                dialog.wait_for(state='hidden')
                page.wait_for_function("getComputedStyle(document.querySelector('#viewport')).overflowY==='auto'")
                assert overflow_state() == before, (style, dialog_id, close, before, overflow_state())
                verify_scroll()
        checks.append('close methods preserve axes/priorities and real wheel scrolling: ' + style)
    frame = load()
    before = overflow_state()
    frame.locator('#execution-policy').click()
    frame.locator('#policy-dialog').wait_for(state='visible')
    page.locator('iframe').evaluate('''iframe => {
        const doc=iframe.contentDocument, win=iframe.contentWindow;
        doc.querySelector('#policy-dialog').close();
        win.eval("openDialog(document.querySelector('#logs-dialog'))");
    }''')
    frame.locator('#logs-dialog').wait_for(state='visible')
    page.wait_for_timeout(100)
    assert page.locator('#viewport').evaluate('e=>getComputedStyle(e).overflowY')=='hidden'
    frame.locator('#logs-dialog .dialog-dismiss').click()
    page.wait_for_function("getComputedStyle(document.querySelector('#viewport')).overflowY==='auto'")
    assert overflow_state()==before
    verify_scroll()
    checks.append('rapid modal handoff keeps lock until the final modal closes')
    # Navigation can remove the frame while a modal remains open. Shared layout
    # nodes survive that removal, so unloading must release their scroll lock.
    for opener in ['#execution-policy', '#logs-open']:
        frame = load()
        before = overflow_state()
        frame.locator(opener).click()
        page.locator('body > button').evaluate('button=>button.click()')
        page.wait_for_function("document.querySelector('iframe')===null")
        assert overflow_state() == before, ('frame unmount leaked scroll lock', before, overflow_state())
        verify_scroll()
    checks.append('iframe removed with either modal open: outer scroll restored')
    assert not errors, errors
    browser.close()
print(json.dumps({'scope': 'local simulated parent and APIs; actual management HTML and Chromium; no production', 'checks': checks}, ensure_ascii=False))
