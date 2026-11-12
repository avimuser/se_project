"""Capture an isolated report demo on port 8093 with a fresh database."""
import os,json,html
from pathlib import Path
from datetime import datetime,timedelta,timezone
from playwright.sync_api import sync_playwright
root=Path(__file__).resolve().parents[1];base='http://localhost:8093';password=os.environ['REPORT_DEMO_PASSWORD'];checks=[]
with sync_playwright() as p:
 b=p.chromium.launch(headless=True,args=['--no-sandbox'])
 def signin(user):
  c=b.new_context(viewport={'width':1280,'height':900});r=c.request.post(base+'/api/auth/login',data={'username':user,'password':password});assert r.status==200,r.text();c.set_extra_http_headers({'X-CSRF-Token':r.json()['csrf']});return c
 def req(c,path,data=None,status=201,method='POST'):
  r=c.request.fetch(base+'/api/'+path,method=method,data=data);assert r.status==status,(path,r.status,r.text());return r.json() if r.status!=204 else None
 a=signin('admin');l=req(a,'laundromats',{'name':'Campus Laundry Centre'})['id'];h=req(a,'hostels',{'name':'North Hostel','laundromat_id':l})['id'];d=req(a,'handlers',{'name':'Demo Handler'})['id']
 req(a,'students/bulk',[{'name':n,'roll_number':i,'hostel_id':h} for n,i in [('Avery Morgan',2026001),('Sam Taylor',2026002),('Jordan Lee',2026003)]])
 for u,r,i in [('report-laundry','laundromat',l),('report-handler','handler',d),('report-student','student',2026002),('report-warden','warden',h)]:req(a,'accounts',{'username':u,'password':password,'role':r,'subject_id':i})
 lw=signin('report-laundry');hd=signin('report-handler');st=signin('report-student');wd=signin('report-warden');when=(datetime.now(timezone.utc)+timedelta(hours=2)).isoformat()
 def schedule(kind,students=[]):return req(lw,'jobs',{'kind':kind,'students':students,'hostel_id':h,'laundromat_id':l,'handler_id':d,'scheduled_time':when})['id']
 def act(j,action,i=0,failed=False,status=200):return req(hd,f'jobs/{j}/{action}',{'handler_id':d,'student_id':i,'failed':failed},status)
 pickup=schedule('pickup');act(pickup,'start');act(pickup,'scan',2026001);act(pickup,'scan',2026001,status=409);act(pickup,'scan',2026002);act(pickup,'complete');checks.append('PASS pickup lifecycle and duplicate rejection')
 delivery=schedule('delivery',[2026001,2026002]);act(delivery,'start');act(delivery,'complete',status=409);act(delivery,'scan',2026001);act(delivery,'scan',2026002,True);checks.append('PASS pending-delivery completion rejected')
 for c,role in [(a,'admin'),(wd,'warden'),(lw,'laundromat'),(hd,'handler'),(st,'student')]:
  page=c.new_page();page.goto(base+'/'+role+'/');page.wait_for_timeout(1200)
  if role=='handler':page.get_by_role('button').filter(has_text=f'Delivery #{delivery}').first.click();page.locator('[aria-label="Job details"]').scroll_into_view_if_needed()
  page.screenshot(path=str(root/'assets'/f'{role}.png'));page.close()
 act(delivery,'complete');retry=schedule('delivery',[2026002]);act(retry,'start');act(retry,'scan',2026002);act(retry,'complete');s=req(st,'workflow?student_id=2026002',status=200,method='GET');assert any(i['result']=='failed' for j in s['jobs'] for i in j['items']);assert not s['bags'];checks.append('PASS failed return, successful retry and retained history')
 req(st,'accounts',status=403,method='GET');checks.append('PASS student denied admin account list');req(st,'notifications/read',{'student_id':2026002},200);checks.append('PASS saved notifications marked read')
 (root/'evidence/browser-checks.json').write_text(json.dumps({'checks':checks,'jobs':[pickup,delivery,retry],'student_job_count':len(s['jobs'])},indent=2)+'\n')
 page=a.new_page();log=(root/'evidence/backend-tests.txt').read_text();page.set_content('<body style="margin:0;padding:25px;background:#111827;color:#e5e7eb;font:16px monospace"><h2 style="color:#a7f3d0">Smart Laundry verification</h2><pre>'+html.escape(log)+'\n'+'\n'.join(checks)+'</pre></body>');page.screenshot(path=str(root/'assets/test-results.png'),full_page=True);b.close()
