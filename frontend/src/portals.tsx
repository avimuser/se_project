import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { WashingMachine, ArrowRight, RefreshCw, QrCode, Camera, X, Check, Package, Bell, Truck, Building2 } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import jsQR from 'jsqr'

export let csrfToken = ''
export function setCSRF(value: string) { csrfToken = value }
export type AuthUser = { id: number; username: string; role: 'admin' | 'warden' | 'laundromat' | 'handler' | 'student'; subject_id: number | null; csrf: string }
export async function api(path: string, method = 'GET', body?: object) {
  const response = await fetch(`/api/${path}`, { method, headers: { ...(body ? { 'Content-Type': 'application/json' } : {}), ...(method !== 'GET' ? { 'X-CSRF-Token': csrfToken } : {}) }, body: body ? JSON.stringify(body) : undefined })
  if (response.status === 401) { window.location.reload(); throw new Error('Session expired') }
  if (!response.ok) { const error = await response.json().catch(() => null); throw new Error(error?.error ?? `Request failed (${response.status})`) }
  return response.status === 204 ? null : response.json()
}
export function PortalNav({ current }: { current: string }) {
  return <nav className="portal-nav" aria-label="Portal"><a href={`/${current}/`} aria-current="page">{current[0].toUpperCase() + current.slice(1)} portal</a></nav>
}
export function useLive(refresh: () => void) {
  const callback = useRef(refresh); callback.current = refresh
  const [connected, setConnected] = useState(false)
  useEffect(() => {
    const stream = new EventSource('/api/events')
    stream.onopen = () => setConnected(true)
    stream.onerror = () => setConnected(false)
    stream.addEventListener('change', () => callback.current())
    return () => stream.close()
  }, [])
  return connected
}
type Person = { id: number; name: string; hostel_id?: number; laundromat_id?: number }
type Directory = { students: Person[]; handlers: Person[]; hostels: Person[]; laundromats: Person[] }
type Item = { student_id: number; name: string; result: string; scanned_at: string | null }
type Job = { id: number; kind: 'pickup' | 'delivery'; hostel_id: number; laundromat_id: number; handler_id: number; scheduled_time: string; status: string; hostel: string; laundromat: string; handler: string; items: Item[] }
type Bag = { student_id: number; name: string; hostel_id: number; laundromat_id: number; pickup_job: number; delivery_job: number | null; archived: boolean }
type Note = { id: number; job_id: number; message: string; created_at: string; read: boolean }
type Snapshot = { jobs: Job[]; bags: Bag[]; notifications: Note[] }
const empty: Snapshot = { jobs: [], bags: [], notifications: [] }
const date = (value: string) => new Date(value).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' })
const notificationText = (message: string) => message.replace(/(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2}))$/, timestamp => date(timestamp))
const label = (value: string) => value[0].toUpperCase() + value.slice(1)
function Status({ value }: { value: string }) { return <span className={`tag status-${value}`}>{label(value)}</span> }

function Scanner({ onScan, onClose }: { onScan: (roll: string) => void; onClose: () => void }) {
  const video = useRef<HTMLVideoElement>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let stopped = false; let media: MediaStream | undefined; let frame = 0; let last = 0
    const canvas = document.createElement('canvas'); const ctx = canvas.getContext('2d', { willReadFrequently: true })
    async function start() {
      try {
        if (!navigator.mediaDevices?.getUserMedia) throw new Error('Camera needs HTTPS or localhost. You can enter a roll number below.')
        media = await navigator.mediaDevices.getUserMedia({ video: { facingMode: { ideal: 'environment' }, width: { ideal: 720 } }, audio: false })
        if (stopped) { media.getTracks().forEach(track => track.stop()); return }
        const element = video.current!; element.srcObject = media; await element.play()
        function tick(now: number) {
          if (stopped) return
          if (ctx && element.readyState >= 2 && now - last > 200) {
            last = now; canvas.width = element.videoWidth; canvas.height = element.videoHeight
            ctx.drawImage(element, 0, 0)
            const pixels = ctx.getImageData(0, 0, canvas.width, canvas.height)
            const code = jsQR(pixels.data, canvas.width, canvas.height, { inversionAttempts: 'attemptBoth' })
            if (code) {
              const match = /^(?:smart-laundry:)?([1-9]\d*)$/.exec(code.data.trim())
              if (match && Number.isSafeInteger(Number(match[1]))) { onScan(match[1]); return }
              setError('This QR is not a Smart Laundry bag code. Try another code or enter the roll number.')
            }
          }
          frame = requestAnimationFrame(tick)
        }
        frame = requestAnimationFrame(tick)
      } catch (e) { if (!stopped) setError((e as Error).message) }
    }
    void start()
    return () => { stopped = true; cancelAnimationFrame(frame); media?.getTracks().forEach(track => track.stop()) }
  }, [onScan])
  return <div className="scanner"><div className="section-heading"><strong>Scan student bag</strong><button type="button" onClick={onClose} aria-label="Close camera"><X size={18}/></button></div><video ref={video} playsInline muted aria-label="Bag QR camera preview"/><p>Point your camera at the student’s bag QR code.</p>{error && <div className="error" role="alert">{error}</div>}</div>
}

export function Operations({ role, user }: { role: 'laundromat' | 'handler' | 'student'; user: AuthUser }) {
  const identity = String(user.subject_id)
  const [directory, setDirectory] = useState<Directory>({ students: [], handlers: [], hostels: [], laundromats: [] })
  const [state, setState] = useState<Snapshot>(empty)
  const [error, setError] = useState(''); const [notice, setNotice] = useState(''); const [loading, setLoading] = useState(true); const [busy, setBusy] = useState(false)
  const [tab, setTab] = useState('upcoming'); const [selectedJob, setSelectedJob] = useState<number | null>(null)
  const [roll, setRoll] = useState(''); const [scanning, setScanning] = useState(false)
  const [kind, setKind] = useState<'pickup' | 'delivery'>('pickup'); const [hostel, setHostel] = useState(''); const [handler, setHandler] = useState(''); const [when, setWhen] = useState(''); const [batch, setBatch] = useState(''); const [selected, setSelected] = useState<number[]>([])
  const generation = useRef(0)
  const refresh = useCallback(async () => {
    const sequence = ++generation.current
    try {
      const paths = role === 'student' ? ['students', 'workflow'] : role === 'handler' ? ['handlers', 'workflow'] : ['laundromats', 'hostels', 'handlers', 'workflow']
      const values = await Promise.all(paths.map(path => api(path)))
      const result = Object.fromEntries(paths.map((path, index) => [path, values[index]]))
      const { students = [], handlers = [], hostels = [], laundromats = [], workflow: snapshot } = result
      if (sequence !== generation.current) return
      setDirectory({ students, handlers, hostels, laundromats }); setState(snapshot); setError('')
    } catch (e) { if (sequence === generation.current) setError((e as Error).message) }
    finally { if (sequence === generation.current) setLoading(false) }
  }, [identity, role])
  const connected = useLive(() => { void refresh() })
  useEffect(() => { void refresh() }, [refresh])
  useEffect(() => { document.title = `Smart Laundry · ${label(role)}` }, [role])
  useEffect(() => { if (notice) { const timer = setTimeout(() => setNotice(''), 6000); return () => clearTimeout(timer) } }, [notice])
  const people = role === 'student' ? directory.students : role === 'handler' ? directory.handlers : directory.laundromats
  const person = people.find(p => p.id === Number(identity))
  const mine = state.jobs.filter(j => role === 'student' || (role === 'handler' ? j.handler_id : j.laundromat_id) === Number(identity))
  const job = mine.find(j => j.id === selectedJob)
  const custody = state.bags.find(b => b.student_id === Number(identity))
  const delivery = mine.find(j => j.id === custody?.delivery_job)
  const pickup = mine.find(j => j.id === custody?.pickup_job)
  const deliveryResult = delivery?.items.find(item => item.student_id === Number(identity))?.result
  const currentStatus = !custody ? 'Ready for your next pickup' : deliveryResult === 'failed' ? 'Returning to the laundromat' : delivery?.status === 'active' ? 'Out for delivery' : delivery ? 'Delivery scheduled' : pickup?.status === 'active' ? 'Collected by your handler' : 'At the laundromat'
  const jobs = mine.filter(j => tab === 'history' ? j.status === 'completed' : j.status !== 'completed')
  const available = state.bags.filter(b => b.laundromat_id === Number(identity) && b.hostel_id === Number(hostel) && !b.delivery_job && state.jobs.find(j => j.id === b.pickup_job)?.status === 'completed')
  const visibleBags = available.filter(b => !batch || b.pickup_job === Number(batch))
  async function mutate(path: string, body: object, message: string) {
    if (busy) return false
    setBusy(true); setError(''); setNotice('')
    try { await api(path, 'POST', body); setNotice(message); await refresh(); return true }
    catch (e) { setError((e as Error).message); return false }
    finally { setBusy(false) }
  }
  async function schedule(event: FormEvent) {
    event.preventDefault()
    if (!when || !person) return
    if (await mutate('jobs', { kind, hostel_id: Number(hostel), laundromat_id: person.id, handler_id: Number(handler), scheduled_time: new Date(when).toISOString(), students: kind === 'delivery' ? selected : [] }, `${label(kind)} scheduled. Students have been notified.`)) { setSelected([]); setWhen('') }
  }
  async function action(name: string, failed = false) {
    if (!job || !person) return
    const id = Number(roll)
    if (name === 'scan' && (!Number.isSafeInteger(id) || id <= 0)) { setError('Enter a valid numeric roll number.'); return }
    if (await mutate(`jobs/${job.id}/${name}`, { handler_id: person.id, ...(name === 'scan' ? { student_id: id, failed } : {}) }, name === 'scan' ? failed ? 'Failed delivery recorded. Return this bag to the laundromat before completing the job.' : job.kind === 'pickup' ? 'Bag collected.' : 'Bag delivered.' : name === 'start' ? 'Job started. Scan a bag or enter its roll number.' : 'Job completed.')) { setRoll(''); setScanning(false) }
  }
  const onScan = useCallback((value: string) => { setRoll(value); setScanning(false); setNotice('QR scanned. Confirm the action for this bag below.') }, [])
  return <>
    <header className="topbar"><a className="brand" href={`/${role}/`}><span className="brandmark"><WashingMachine size={23}/></span>Smart Laundry</a><div className="profile"><span className="avatar">{label(role)[0]}</span><span>{label(role)}</span></div></header>
    <main className={`operations ${role}`}>
      <PortalNav current={role}/>
      <div className="section-heading"><div><span className="eyebrow">CAMPUS LAUNDRY</span><h1>{role === 'student' ? 'Your laundry, at a glance' : role === 'handler' ? 'Your pickup & delivery route' : 'Laundry operations'}</h1><p>{role === 'student' ? 'Your bag code, live updates and laundry history.' : role === 'handler' ? 'Collect bags, make deliveries and keep everyone updated.' : 'Schedule campus pickups and get clean bags back to students.'}</p></div><span className={`live ${connected ? 'connected' : ''}`} role="status">{connected ? 'Live updates on' : 'Reconnecting…'}</span></div>
      <div className="identity-bar"><div className="identity-name"><span className="eyebrow">SIGNED IN AS</span><strong>{person?.name ?? user.username}</strong></div><button onClick={() => void refresh()} disabled={loading || busy} aria-label="Refresh"><RefreshCw size={18}/></button></div>
      {error && <div className="error" role="alert">{error}<button onClick={() => void refresh()}>Retry</button></div>}{notice && <div className="success" role="status">{notice}</div>}
      {loading ? <div className="empty">Loading your laundry…</div> : !person ? <div className="empty"><Building2 size={32}/><h3>{people.length ? `Select your ${role} to continue` : `No ${role === 'student' ? 'students' : role === 'handler' ? 'handlers' : 'laundromats'} registered`}</h3><p>{people.length ? 'Choose from the list above.' : 'Ask your campus administrator to set up your record.'}</p></div> : <>
      {role === 'student' && <div className="student-overview"><section className="panel qr-panel"><span className="eyebrow">YOUR BAG CODE</span><h2>{person.name}</h2><div className="qr-code"><QRCodeSVG value={`smart-laundry:${person.id}`} size={200} level="M" title={`Bag QR for roll number ${person.id}`} marginSize={3}/></div><strong className="roll-number">{person.id}</strong><p>Show this code to your handler at pickup or delivery.</p><button onClick={() => window.print()}><QrCode size={17}/>Print bag code</button></section><section className="panel"><span className="eyebrow">CURRENT STATUS</span><h2>{currentStatus}</h2><p>{custody ? `Bag collected in pickup batch #${custody.pickup_job}. ${custody.delivery_job ? 'Check the delivery below for its progress.' : 'Your bag is waiting for its next delivery.'}` : 'Your bag is with you. Scheduled pickups appear below.'}</p><div className="notifications"><div className="section-heading"><h3><Bell size={17}/>Notifications {state.notifications.some(n => !n.read) && <span className="tag">New</span>}</h3>{state.notifications.some(n => !n.read) && <button disabled={busy} onClick={() => void mutate('notifications/read', { student_id: person.id }, 'Notifications marked as read.')}>Mark read</button>}</div>{state.notifications.length ? state.notifications.slice(0, 30).map(n => <article className={`notification ${n.read ? '' : 'unread'}`} key={n.id}><p>{notificationText(n.message)}</p><small>{date(n.created_at)}</small></article>) : <p>No notifications yet. Scheduling and bag updates will appear here.</p>}</div></section></div>}
      {role === 'laundromat' && <section className="panel scheduling"><div className="section-heading"><div><h2>Schedule a {kind}</h2><p>{kind === 'pickup' ? 'Choose a hostel and assign a handler. Scanned bags form the pickup batch.' : 'Select collected bags for delivery. Filter by their pickup batch.'}</p></div><div className="segmented"><button className={kind === 'pickup' ? 'active' : ''} onClick={() => { setKind('pickup'); setSelected([]) }}>Pickup</button><button className={kind === 'delivery' ? 'active' : ''} onClick={() => { setKind('delivery'); setSelected([]) }}>Delivery</button></div></div><form onSubmit={schedule}><div className="form-grid"><label className="field">Hostel<select aria-label="Hostel" required value={hostel} onChange={e => { setHostel(e.target.value); setSelected([]); setBatch('') }}><option value="">Select hostel</option>{directory.hostels.filter(h => h.laundromat_id === person.id).map(h => <option key={h.id} value={h.id}>{h.name}</option>)}</select></label><label className="field">Handler<select aria-label="Assigned handler" required value={handler} onChange={e => setHandler(e.target.value)}><option value="">Assign handler</option>{directory.handlers.map(h => <option key={h.id} value={h.id}>{h.name}</option>)}</select></label><label className="field">Scheduled date & time<input required type="datetime-local" value={when} onChange={e => setWhen(e.target.value)}/></label></div>{kind === 'delivery' && <><label className="field">Pickup batch<select aria-label="Pickup batch" value={batch} onChange={e => { setBatch(e.target.value); setSelected([]) }}><option value="">All available batches</option>{[...new Set(available.map(b => b.pickup_job))].map(id => <option key={id} value={id}>Batch #{id} · {date(state.jobs.find(j => j.id === id)!.scheduled_time)}</option>)}</select></label><div className="bag-selection">{visibleBags.map(b => <label key={b.student_id} className="bag-choice"><input type="checkbox" checked={selected.includes(b.student_id)} onChange={e => setSelected(current => e.target.checked ? [...current, b.student_id] : current.filter(id => id !== b.student_id))}/><span><strong>{b.name}{b.archived ? ' (removed student)' : ''}</strong><small>Roll {b.student_id} · Batch #{b.pickup_job}</small></span></label>)}</div>{!visibleBags.length && <p className="hint">{hostel ? 'No bags available. Complete a pickup or return failed deliveries first.' : 'Choose a hostel to see its available bags.'}</p>}<p>{selected.length} bags selected</p></>}<button className="primary" disabled={busy || !hostel || !handler || (kind === 'delivery' && !selected.length)}><PlusIcon/>{busy ? 'Scheduling…' : `Schedule ${kind}`}</button></form></section>}
      <section className="jobs-section"><div className="section-heading"><h2>{role === 'student' ? 'Your laundry' : role === 'handler' ? 'Assigned jobs' : 'Pickups & deliveries'}</h2><div className="segmented"><button className={tab === 'upcoming' ? 'active' : ''} onClick={() => setTab('upcoming')}>Upcoming & active</button><button className={tab === 'history' ? 'active' : ''} onClick={() => setTab('history')}>History</button></div></div><div className="job-grid">{jobs.map(j => <button key={j.id} className={`job-card ${selectedJob === j.id ? 'selected' : ''}`} onClick={() => { setSelectedJob(j.id); setRoll(''); setScanning(false) }}><span className="job-card-top"><span className="row-icon">{j.kind === 'pickup' ? <Package size={20}/> : <Truck size={20}/>}</span><Status value={j.status}/></span><strong>{label(j.kind)} #{j.id}</strong><span>{j.hostel} · {j.laundromat}</span><span>{date(j.scheduled_time)}</span><span className="job-card-bottom">{role === 'handler' ? `${j.items.length} bags` : `Handler: ${j.handler}`}<ArrowRight size={16}/></span></button>)}</div>{!jobs.length && <div className="empty"><Package size={30}/><h3>{tab === 'history' ? 'No completed jobs yet' : 'No upcoming jobs'}</h3><p>{role === 'student' ? 'You’ll receive a live update when your laundry is scheduled.' : 'Scheduled work will appear here.'}</p></div>}</section>
      {job && <section className="panel job-detail" aria-label="Job details"><div className="section-heading"><div><span className="eyebrow">{job.kind === 'pickup' ? 'PICKUP BATCH' : 'DELIVERY'}</span><h2>{label(job.kind)} #{job.id} · {job.hostel}</h2><p>{date(job.scheduled_time)} · {job.handler}</p></div><Status value={job.status}/></div>
        {role === 'handler' && job.status === 'scheduled' && <button className="primary" disabled={busy} onClick={() => void action('start')}><ArrowRight size={18}/>Start {job.kind}</button>}
        {role === 'handler' && job.status === 'active' && <div className="handler-actions"><p>{job.kind === 'pickup' ? 'Scan or enter a student’s roll number to collect their bag.' : 'Scan or enter the roll number, then record the delivery result. Return failed bags before completing this route.'}</p>{scanning && <Scanner onScan={onScan} onClose={() => setScanning(false)}/>}<form onSubmit={e => { e.preventDefault(); void action('scan') }}><label className="field">Roll number<input inputMode="numeric" pattern="[0-9]+" required value={roll} onChange={e => setRoll(e.target.value)} placeholder="Scan QR or enter roll number" disabled={busy}/></label><div className="scan-buttons"><button type="button" disabled={busy} onClick={() => setScanning(value => !value)}><Camera size={18}/>{scanning ? 'Close camera' : 'Scan QR'}</button><button className="primary" disabled={busy || !roll}><Check size={18}/>{job.kind === 'pickup' ? 'Collect bag' : 'Confirm delivered'}</button>{job.kind === 'delivery' && <button className="danger" type="button" disabled={busy || !roll} onClick={() => void action('scan', true)}>Student unavailable</button>}</div></form><div className="complete-action"><button disabled={busy || job.items.some(i => i.result === 'pending')} onClick={() => void action('complete')}><Check size={18}/>{job.kind === 'delivery' && job.items.some(i => i.result === 'failed') ? 'Bags returned · Complete delivery' : `Complete ${job.kind}`}</button>{job.items.some(i => i.result === 'pending') && <p>Process all assigned bags to complete this delivery.</p>}</div></div>}
        <h3>{job.kind === 'pickup' ? 'Bags collected in this batch' : 'Delivery bags'}</h3><div className="item-list">{job.items.map(item => <article className="bag-row" key={item.student_id}><span><strong>{item.name}</strong><small>Roll {item.student_id}{item.scanned_at ? ` · ${date(item.scanned_at)}` : ''}</small></span><Status value={item.result}/></article>)}</div>{!job.items.length && <p>No bags collected yet.</p>}
      </section>}
      </>}
    </main>
  </>
}
function PlusIcon() { return <Package size={18}/> }
