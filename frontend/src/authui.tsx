import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { WashingMachine, LogOut, UserRound, ShieldCheck, KeyRound, Plus, X } from 'lucide-react'
import { api, setCSRF, type AuthUser } from './portals'

type Account = { id: number; username: string; role: AuthUser['role']; subject_id: number | null; active: boolean }
type Record = { id: number; name: string }
type Directory = { students: Record[]; handlers: Record[]; hostels: Record[]; laundromats: Record[] }
const roles: AuthUser['role'][] = ['admin','warden','laundromat','handler','student']
export function AuthGate({ role, children }: { role: string; children: (user: AuthUser) => ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null)
  const [loading, setLoading] = useState(true)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [changing, setChanging] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  useEffect(() => { fetch('/api/auth/me', { cache:'no-store' }).then(async r => r.ok ? r.json() : null).then(account => { if (account) { setCSRF(account.csrf); setUser(account) } }).catch(() => setError('Could not reach the server.')).finally(() => setLoading(false)) }, [])
  async function login(e: FormEvent) {
    e.preventDefault(); setBusy(true); setError('')
    try {
      const response = await fetch('/api/auth/login', { method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({username,password}) })
      const body = await response.json()
      if (!response.ok) throw new Error(body.error ?? 'Sign-in failed')
      setCSRF(body.csrf); setUser(body); setPassword('')
      if (body.role !== role) window.location.replace(`/${body.role}/`)
    } catch (e) { setError((e as Error).message) } finally { setBusy(false) }
  }
  async function logout() { try { await api('auth/logout','POST') } finally { setCSRF('');setUser(null);setChanging(false);window.location.replace(`/${role}/`) } }
  async function changePassword(e: FormEvent) {
    e.preventDefault();setBusy(true);setError('');setNotice('')
    try { await api('auth/password','POST',{old_password:oldPassword,new_password:newPassword});setChanging(false);setOldPassword('');setNewPassword('');setNotice('Password changed. Other sessions have been signed out.') }
    catch(e){setError((e as Error).message)} finally{setBusy(false)}
  }
  if (loading) return <div className="auth-screen"><div className="auth-card">Loading Smart Laundry…</div></div>
  if (!user) return <div className="auth-screen"><form className="auth-card" onSubmit={login}><span className="brandmark"><WashingMachine size={25}/></span><span className="eyebrow">SMART LAUNDRY</span><h1>Welcome back</h1><p>Sign in to continue to your portal.</p><label className="field">Username<input autoComplete="username" required value={username} onChange={e => setUsername(e.target.value)} autoFocus/></label><label className="field">Password<input type="password" autoComplete="current-password" required value={password} onChange={e => setPassword(e.target.value)}/></label>{error && <div className="error" role="alert">{error}</div>}<button className="primary" disabled={busy}>{busy?'Signing in…':'Sign in'}</button></form></div>
  if (user.role !== role) { window.location.replace(`/${user.role}/`);return <div className="auth-screen">Opening your portal…</div> }
  return <><div className="account-strip"><span><ShieldCheck size={15}/> {user.username} · {user.role}</span><button onClick={() => {setChanging(value=>!value);setError('')}}><KeyRound size={15}/>Password</button><button onClick={() => void logout()}><LogOut size={15}/>Sign out</button></div>{notice && <div className="account-message success" role="status">{notice}</div>}{changing && <div className="account-password"><form onSubmit={changePassword}><div className="section-heading"><h2>Change password</h2><button type="button" onClick={()=>setChanging(false)} aria-label="Close"><X size={18}/></button></div><label className="field">Current password<input type="password" autoComplete="current-password" required value={oldPassword} onChange={e=>setOldPassword(e.target.value)}/></label><label className="field">New password<input type="password" autoComplete="new-password" minLength={12} maxLength={128} required value={newPassword} onChange={e=>setNewPassword(e.target.value)}/></label>{error && <div className="error" role="alert">{error}</div>}<button className="primary" disabled={busy}>Save password</button></form></div>}{children(user)}</>
}
export function Accounts({ directory, warden = false }: { directory: Directory; warden?: boolean }) {
  const [accounts,setAccounts]=useState<Account[]>([])
  const [role,setRole]=useState<AuthUser['role']>('student')
  const [subject,setSubject]=useState('')
  const [username,setUsername]=useState('')
  const [password,setPassword]=useState('')
  const [error,setError]=useState('')
  const [notice,setNotice]=useState('')
  const [busy,setBusy]=useState(false)
  const [reset,setReset]=useState<number|null>(null)
  const [resetValue,setResetValue]=useState('')
  const records = role === 'student' ? directory.students : role === 'handler' ? directory.handlers : role === 'warden' ? directory.hostels : role === 'laundromat' ? directory.laundromats : []
  async function refresh(){try{setAccounts(await api('accounts'))}catch(e){setError((e as Error).message)}}
  useEffect(()=>{void refresh()},[])
  async function add(e:FormEvent){e.preventDefault();setBusy(true);setError('');setNotice('');try{await api('accounts','POST',{username,password,role,subject_id:role==='admin'?null:Number(subject)});setNotice('Account created. Share the credentials directly with the owner.');setUsername('');setPassword('');setSubject('');await refresh()}catch(e){setError((e as Error).message)}finally{setBusy(false)}}
  async function revoke(account:Account){if(!window.confirm(`Deactivate ${account.username}? Their existing sessions will end.`))return;setBusy(true);try{await api(`accounts/${account.id}`,'DELETE');setNotice(`${account.username} deactivated.`);await refresh()}catch(e){setError((e as Error).message)}finally{setBusy(false)}}
  async function resetPassword(e:FormEvent){e.preventDefault();if(reset===null)return;setBusy(true);setError('');try{await api(`accounts/${reset}`,'PUT',{password:resetValue});setNotice('Password reset. Existing sessions have ended.');setResetValue('');setReset(null)}catch(e){setError((e as Error).message)}finally{setBusy(false)}}
  return <section className="management accounts" aria-label="Accounts"><div className="section-heading"><div><h2>Portal accounts</h2><p>Create an account for each person or device and assign it to a campus record.</p></div><UserRound size={23}/></div>{error&&<div className="error" role="alert">{error}</div>}{notice&&<div className="success" role="status">{notice}</div>}<form className="account-form" onSubmit={add}><label className="field">Role<select value={role} onChange={e=>{setRole(e.target.value as AuthUser['role']);setSubject('')}}>{(warden ? (['student'] as AuthUser['role'][]) : roles).map(item=><option key={item} value={item}>{item[0].toUpperCase()+item.slice(1)}</option>)}</select></label>{role!=='admin'&&<label className="field">Assigned record<select required value={subject} onChange={e=>setSubject(e.target.value)}><option value="">Select record</option>{records.map(item=><option key={item.id} value={item.id}>{item.name} · {item.id}</option>)}</select></label>}<label className="field">Username<input required minLength={3} maxLength={64} autoComplete="off" value={username} onChange={e=>setUsername(e.target.value)}/></label><label className="field">Temporary password<input required type="password" minLength={12} maxLength={128} autoComplete="new-password" value={password} onChange={e=>setPassword(e.target.value)}/></label><button className="primary" disabled={busy||role!=='admin'&&!subject}><Plus size={17}/>Create account</button></form><div className="account-list">{accounts.filter(a=>a.active).map(account=><div className="bag-row" key={account.id}><span><strong>{account.username}</strong><small>{account.role}{account.subject_id ? ` · #${account.subject_id}` : ''}</small></span><div className="account-actions"><button onClick={()=>{setReset(account.id);setResetValue('');setError('')}}>Reset password</button><button className="danger-icon" onClick={()=>void revoke(account)}>Deactivate</button></div></div>)}</div>{reset!==null&&<form className="reset-password" onSubmit={resetPassword}><h3>Reset password for {accounts.find(a=>a.id===reset)?.username}</h3><label className="field">New password<input autoFocus type="password" minLength={12} maxLength={128} required value={resetValue} onChange={e=>setResetValue(e.target.value)}/></label><button className="primary" disabled={busy}>Reset password</button><button type="button" onClick={()=>setReset(null)}>Cancel</button></form>}</section>
}
