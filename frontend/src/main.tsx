import { useEffect, useRef, useState, type FormEvent } from 'react'
import { createRoot } from 'react-dom/client'
import { WashingMachine, Building2, GraduationCap, Users, Plus, Search, Pencil, Trash2, X, ArrowRight, RefreshCw } from 'lucide-react'
import '@fontsource/inter/400.css'
import '@fontsource/inter/500.css'
import '@fontsource/inter/600.css'
import './style.css'
import { Operations, PortalNav, useLive, api, type AuthUser } from './portals'
import { AuthGate, Accounts } from './authui'

type Kind = 'laundromats' | 'hostels' | 'students' | 'handlers'
type Record = { id: number; name: string; laundromat_id?: number; hostel_id?: number }
type Data = { [K in Kind]: Record[] }
const kinds: Kind[] = ['laundromats', 'hostels', 'students', 'handlers']
const meta = {
  laundromats: { title: 'Laundromats', singular: 'laundromat', description: 'Set up laundromats.', icon: WashingMachine },
  hostels: { title: 'Hostels', singular: 'hostel', description: 'Create hostels and assign their laundry service.', icon: Building2 },
  students: { title: 'Students', singular: 'student', description: 'Register students and manage their hostel assignments.', icon: GraduationCap },
  handlers: { title: 'Handlers', singular: 'handler', description: 'Manage people taking care of pickups and deliveries.', icon: Users },
}
const warden = window.location.pathname.startsWith('/warden')
document.title = `Smart Laundry · ${warden ? 'Warden' : 'Admin'}`
const empty: Data = { laundromats: [], hostels: [], students: [], handlers: [] }
function App({ user }: { user: AuthUser }) {
  const [active, setActive] = useState<Kind>('students')
  const [data, setData] = useState<Data>(empty)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [search, setSearch] = useState('')
  const [hostelFilter, setHostelFilter] = useState(warden ? String(user.subject_id) : '')
  const [editor, setEditor] = useState<{ kind: Kind; record?: Record } | null>(null)
  const [removing, setRemoving] = useState<Record | null>(null)
  const [roll, setRoll] = useState('')
  const [bulk, setBulk] = useState(false)
  const [bulkText, setBulkText] = useState('')
  const [name, setName] = useState('')
  const [parent, setParent] = useState('')
  const [busy, setBusy] = useState(false)
  const [formError, setFormError] = useState('')
  const dialog = useRef<HTMLDialogElement>(null)
  const trigger = useRef<HTMLElement | null>(null)
  const info = meta[active]
  async function refresh() {
    setLoading(true); setError('')
    try {
      const visible = warden ? (['students', 'hostels'] as Kind[]) : kinds
      const lists = await Promise.all(visible.map(kind => api(kind)))
      setData({ ...empty, ...Object.fromEntries(visible.map((kind, i) => [kind, lists[i]])) } as Data)
    } catch (e) { setError((e as Error).message) }
    finally { setLoading(false) }
  }
  useEffect(() => { void refresh() }, [])
  useLive(() => { void refresh() })
  useEffect(() => {
    if (editor || removing) dialog.current?.showModal()
    else if (dialog.current?.open) { dialog.current.close(); trigger.current?.focus() }
  }, [editor, removing])
  useEffect(() => { if (notice) { const timer = setTimeout(() => setNotice(''), 5000); return () => clearTimeout(timer) } }, [notice])
  function close() { if (!busy) { setEditor(null); setRemoving(null); setFormError('') } }
  function edit(record?: Record) {
    trigger.current = document.activeElement as HTMLElement
    setRoll(record ? String(record.id) : ''); setBulk(false); setBulkText(''); setName(record?.name ?? ''); setParent(String(record?.hostel_id ?? record?.laundromat_id ?? (warden ? hostelFilter : '')))
    setFormError(''); setEditor({ kind: active, record })
  }
  function changeTab(kind: Kind) { setActive(kind); setSearch(''); setHostelFilter(''); setNotice('') }
  async function save(event: FormEvent) {
    event.preventDefault(); if (!editor || busy) return
    setBusy(true); setFormError('')
    try {
      const body = { ...(editor.kind === 'students' ? { roll_number: Number(roll) } : {}), name: name.trim(), ...(editor.kind === 'students' ? { hostel_id: parent ? Number(parent) : null } : editor.kind === 'hostels' ? { laundromat_id: parent ? Number(parent) : null } : {}) }
      if (bulk) {
        const batch = JSON.parse(bulkText)
        if (!Array.isArray(batch) || !batch.length) throw new Error('Enter a nonempty JSON array.')
        const payload = warden ? batch.map(item => ({ ...item, hostel_id: parent ? Number(parent) : null })) : batch
        await api(`${editor.kind}/bulk`, 'POST', payload)
        setEditor(null); setNotice('Records added successfully.'); await refresh(); return
      }
      const saved = await api(`${editor.kind}${editor.record ? `/${editor.record.id}` : ''}`, editor.record ? 'PUT' : 'POST', body)
      setData(current => ({ ...current, [editor.kind]: editor.record ? current[editor.kind].map(row => row.id === saved.id ? saved : row) : [saved, ...current[editor.kind]] }))
      setNotice(`${meta[editor.kind].singular[0].toUpperCase() + meta[editor.kind].singular.slice(1)} ${editor.record ? 'updated' : 'added'} successfully.`)
      setEditor(null)
    } catch (e) { setFormError((e as Error).message) }
    finally { setBusy(false) }
  }
  async function remove() {
    if (!removing || busy) return
    setBusy(true); setFormError('')
    try {
      await api(`${active}/${removing.id}`, 'DELETE')
      setData(current => ({ ...current, [active]: current[active].filter(row => row.id !== removing.id) }))
      setNotice(`${removing.name} removed. Related records and history are preserved.`); setRemoving(null); await refresh()
    } catch (e) { setFormError((e as Error).message) }
    finally { setBusy(false) }
  }
  const hostelName = (id?: number) => data.hostels.find(row => row.id === id)?.name ?? 'Unassigned hostel'
  const laundryName = (id?: number) => data.laundromats.find(row => row.id === id)?.name ?? 'Unassigned laundromat'
  const rows = data[active].filter(row => (!warden || (!!hostelFilter && row.hostel_id === Number(hostelFilter))) && `${row.name} ${row.id} ${active === 'students' ? hostelName(row.hostel_id) : active === 'hostels' ? laundryName(row.laundromat_id) : ''}`.toLowerCase().includes(search.toLowerCase()) && (active !== 'students' || !hostelFilter || row.hostel_id === Number(hostelFilter)))
  const needsParent = warden && !hostelFilter ? true : active === 'students' ? data.hostels.length === 0 : active === 'hostels' ? data.laundromats.length === 0 : false
  const Icon = info.icon
  return <>
    <header className="topbar"><a className="brand" href="/" aria-label="Smart Laundry home"><span className="brandmark"><WashingMachine size={23}/></span><span>Smart Laundry</span></a><div className="profile"><span className="avatar">A</span><span>{warden ? 'Warden' : 'Administrator'}</span></div></header>
    <main><PortalNav current={warden ? 'warden' : 'admin'}/><div className="section-heading"><div><span className="eyebrow">SMART LAUNDRY</span><h1>{warden ? 'Warden portal' : 'Campus administration'}</h1><p>{warden ? 'Select your hostel to register and remove its students.' : 'Manage your campus laundry network.'}</p></div></div>

      {!warden && <div className="stats" aria-label="Campus overview">{kinds.map(kind => { const StatIcon = meta[kind].icon; return <button key={kind} className={`stat ${active === kind ? 'selected' : ''}`} onClick={() => changeTab(kind)}><span className="stat-label"><StatIcon size={18}/>{meta[kind].title}<ArrowRight className="stat-arrow" size={16}/></span><strong>{loading ? '—' : data[kind].length.toLocaleString()}</strong></button> })}</div>}
      {/* <nav className="tabs" aria-label="Manage records">{kinds.map(kind => { const TabIcon = meta[kind].icon; return <button key={kind} aria-current={active === kind ? 'page' : undefined} className={active === kind ? 'active' : ''} onClick={() => changeTab(kind)}><TabIcon size={17}/>{meta[kind].title}<span>{loading ? '—' : data[kind].length}</span></button> })}</nav> */}
      <section className="management" aria-labelledby="list-heading">
        <div className="section-heading"><div><h2 id="list-heading">Manage {info.title.toLowerCase()}</h2><p>{info.description}</p></div><button className="primary" disabled={loading || !!error || needsParent} onClick={() => edit()}><Plus size={18}/>Add {info.singular}</button></div>
        {notice && <div className="success" role="status">{notice}</div>}
        {error ? <div className="error" role="alert">Couldn’t load your campus. {error}<button onClick={() => void refresh()}><RefreshCw size={15}/>Try again</button></div> : <>
          {needsParent && !warden && !loading && <div className="setup">{active === 'students' ? 'Add a hostel before registering students.' : 'Add a laundromat before creating hostels.'}<button onClick={() => changeTab(active === 'students' ? 'hostels' : 'laundromats')}>Set up {active === 'students' ? 'hostels' : 'laundromats'}<ArrowRight size={15}/></button></div>}
          <div className="toolbar"><label className="search"><Search size={18}/><input aria-label={`Search ${info.title.toLowerCase()}`} placeholder={`Search ${info.title.toLowerCase()}…`} value={search} onChange={e => setSearch(e.target.value)}/>{search && <button aria-label="Clear search" onClick={() => setSearch('')}><X size={15}/></button>}</label>{active === 'students' && !warden && <select aria-label="Filter by hostel" value={hostelFilter} onChange={e => setHostelFilter(e.target.value)}><option value="">All hostels</option>{data.hostels.map(row => <option key={row.id} value={row.id}>{row.name}</option>)}</select>}<button className="refresh" disabled={loading} onClick={() => void refresh()} aria-label="Refresh records"><RefreshCw size={17} className={loading ? 'spin' : ''}/></button></div>
          <div className="table-wrap"><table><thead><tr><th>{info.singular === 'student' ? 'Student name' : 'Name'}</th><th>{active === 'students' ? 'Hostel' : active === 'hostels' ? 'Laundromat' : 'Record ID'}</th>{active === 'students' && <th>Roll number / Bag ID</th>}<th className="actions-heading">Actions</th></tr></thead><tbody>{!loading && rows.map(row => <tr key={row.id}><td><span className="record-name"><span className={`row-icon ${active === 'students' ? 'initial' : ''}`}>{active === 'students' ? row.name.slice(0, 2).toUpperCase() : <Icon size={18}/>}</span>{row.name}</span></td><td>{active === 'students' ? <span className="tag"><Building2 size={13}/>{hostelName(row.hostel_id)}</span> : active === 'hostels' ? laundryName(row.laundromat_id) : <span className="record-id">#{String(row.id).padStart(4, '0')}</span>}</td>{active === 'students' && <td className="record-id">#{String(row.id).padStart(4, '0')}</td>}<td><div className="row-actions">{!warden && <button aria-label={`Edit ${row.name}`} title="Edit" onClick={() => edit(row)}><Pencil size={16}/></button>}<button className="danger-icon" aria-label={`Remove ${row.name}`} title="Remove" onClick={() => { trigger.current = document.activeElement as HTMLElement; setFormError(''); setRemoving(row) }}><Trash2 size={16}/></button></div></td></tr>)}</tbody></table>
          {(loading || !rows.length) && <div className="empty"><span className="empty-icon"><Icon size={27}/></span><h3>{loading ? 'Loading your campus…' : search || hostelFilter ? 'No matching records' : `No ${info.title.toLowerCase()} yet`}</h3><p>{loading ? 'Getting everything ready for you.' : search || hostelFilter ? 'Try another search or change your filter.' : `Add your first ${info.singular} to get started.`}</p>{!loading && !search && !hostelFilter && !needsParent && <button className="primary" onClick={() => edit()}><Plus size={16}/>Add {info.singular}</button>}</div>}</div>
          <div className="table-footer"><span>{loading ? 'Loading…' : `${rows.length} of ${data[active].length} ${info.title.toLowerCase()}`}</span></div>
        </>}
      </section>
      <Accounts directory={data} warden={warden} />
    </main>
    <dialog ref={dialog} aria-labelledby="dialog-title" onCancel={e => { e.preventDefault(); close() }} onClick={e => { if (e.target === e.currentTarget) close() }}>
      <div className="dialog-content"><button className="close" aria-label="Close dialog" disabled={busy} onClick={close}><X size={20}/></button><span className="eyebrow">CAMPUS MANAGEMENT</span><h2 id="dialog-title">{removing ? `Remove ${info.singular}?` : `${editor?.record ? 'Edit' : 'Add'} ${editor ? meta[editor.kind].singular : ''}`}</h2>
      {removing ? <><p>Remove <strong>{removing.name}</strong> from your campus? Their record and laundry history will be retained. Related students or hostels will become unassigned.</p>{formError && <div className="error" role="alert">{formError}</div>}<div className="dialog-actions"><button disabled={busy} onClick={close}>Cancel</button><button className="danger" disabled={busy} onClick={() => void remove()}>{busy ? 'Removing…' : 'Remove'}</button></div></> : <form onSubmit={save}><p>{editor && meta[editor.kind].description}</p>{!editor?.record && <label className="field"><span><input type="checkbox" checked={bulk} onChange={e => setBulk(e.target.checked)}/> Bulk creation</span></label>}{bulk ? <label className="field">Records (JSON array)<textarea required rows={8} value={bulkText} onChange={e => setBulkText(e.target.value)} placeholder={'[{"name":"Student name","roll_number":2026001,"hostel_id":1}]'}/></label> : <>{editor?.kind === 'students' && <label className="field">Roll number<input type="number" min="1" max="9007199254740991" required value={roll} disabled={busy || !!editor.record} onChange={e => setRoll(e.target.value)}/></label>}<label className="field">Name<input autoFocus required maxLength={120} value={name} onChange={e => setName(e.target.value)} placeholder={editor?.kind === 'students' ? 'Student’s full name' : 'Enter a name'} disabled={busy}/></label></>}{(editor?.kind === 'students' || editor?.kind === 'hostels') && <label className="field">{editor.kind === 'students' ? 'Hostel' : 'Laundromat'}<select required={warden} value={parent} onChange={e => setParent(e.target.value)} disabled={busy}><option value="">Unassigned</option>{data[editor.kind === 'students' ? 'hostels' : 'laundromats'].map(row => <option key={row.id} value={row.id}>{row.name}</option>)}</select></label>}{formError && <div className="error" role="alert">{formError}</div>}<div className="dialog-actions"><button type="button" disabled={busy} onClick={close}>Cancel</button><button className="primary" disabled={busy || (bulk ? !bulkText.trim() : !name.trim())}>{busy ? 'Saving…' : editor?.record ? 'Save changes' : `Add ${editor ? meta[editor.kind].singular : ''}`}</button></div></form>}</div>
    </dialog>
  </>
}

const role = window.location.pathname.split('/')[1]
createRoot(document.getElementById('root')!).render(<AuthGate role={role}>{user => role === 'laundromat' || role === 'handler' || role === 'student' ? <Operations role={role} user={user}/> : <App user={user}/>}</AuthGate>)
