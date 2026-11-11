import React, { useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';

const basic = (username, password) => `Basic ${btoa(`${username}:${password}`)}`;
const stamp = (value) => value ? new Date(value).toLocaleString() : '—';

function App() {
  const [credentials, setCredentials] = useState(null);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [reports, setReports] = useState([]);
  const [counts, setCounts] = useState({ open: 0, actioned: 0, dismissed: 0 });
  const [filter, setFilter] = useState('open');
  const [offset, setOffset] = useState(0);
  const [selected, setSelected] = useState(null);
  const [notes, setNotes] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const auth = credentials ? basic(credentials.username, credentials.password) : '';
  async function api(path, options = {}, authValue = auth) {
    const response = await fetch(path, {
      ...options,
      headers: { ...(options.body ? { 'Content-Type': 'application/json' } : {}), Authorization: authValue, ...options.headers },
    });
    if (response.status === 401) throw new Error('Your moderator credentials were rejected. Please sign in again.');
    const body = response.status === 204 ? null : await response.json().catch(() => null);
    if (!response.ok) throw new Error(body?.error || `Request failed (${response.status})`);
    return body;
  }

  async function load(authValue = auth, syncSelection = true) {
    setBusy(true); setError('');
    try {
      const query = filter === 'all' ? `?limit=100&offset=${offset}` : `?status=${filter}&limit=100&offset=${offset}`;
      const [body, summary] = await Promise.all([
        api(`/v1/admin/reports${query}`, {}, authValue),
        api('/v1/admin/reports/summary', {}, authValue),
      ]);
      setReports(body.reports || []);
      setCounts(summary);
      if (syncSelection && selected) setSelected((body.reports || []).find((item) => item.report_id === selected.report_id) || null);
    } catch (err) { setError(err.message); if (/credentials were rejected/.test(err.message)) setCredentials(null); }
    finally { setBusy(false); }
  }

  useEffect(() => { if (credentials) load(); }, [credentials, filter, offset]);

  async function login(event) {
    event.preventDefault(); setError(''); setBusy(true);
    const authValue = basic(username.trim(), password);
    try {
      await api('/v1/admin/session', { method: 'POST' }, authValue);
      setCredentials({ username: username.trim(), password }); setPassword('');
    } catch (err) { setError(err.message); }
    finally { setBusy(false); }
  }

  async function review(status) {
    if (!selected) return;
    setBusy(true); setError('');
    try {
      await api(`/v1/admin/reports/${selected.report_id}`, { method: 'PUT', body: JSON.stringify({ status, notes }) });
      setSelected(null); setNotes(''); await load(auth, false);
    } catch (err) { setError(err.message); }
    finally { setBusy(false); }
  }

  const visibleReports = reports;
  const resultCount = filter === 'all' ? counts.open + counts.actioned + counts.dismissed : counts[filter];

  if (!credentials) return <main className="login-wrap">
    <div className="login-card">
      <div className="brand-mark">P</div>
      <p className="eyebrow">PINGMESSENGER TRUST & SAFETY</p>
      <h1>Moderator sign in</h1>
      <p className="muted">Use your pre-created moderator account to review user reports.</p>
      <form onSubmit={login}>
        <label>Username<input autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} required /></label>
        <label>Password<input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required /></label>
        {error && <p className="error">{error}</p>}
        <button className="primary full" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
      </form>
      <p className="footnote">Moderator access is managed by your administrator.</p>
    </div>
  </main>;

  return <div className="shell">
    <aside className="sidebar">
      <div className="brand"><div className="brand-mark small">P</div><span>PingMessenger</span></div>
      <div className="workspace-label">WORKSPACE</div>
      <button className="nav-item active"><span>▦</span> Reports <b>{counts.open}</b></button>
      <div className="sidebar-bottom"><div className="avatar">{credentials.username.slice(0, 1).toUpperCase()}</div><div className="user"><strong>{credentials.username}</strong><small>Moderator</small></div><button className="logout" title="Sign out" onClick={() => { setCredentials(null); setReports([]); setSelected(null); }}>↪</button></div>
    </aside>
    <main className="main">
      <header className="topbar"><div><span className="eyebrow">TRUST & SAFETY</span><h1>Reports</h1></div><button className="refresh" onClick={() => load()} disabled={busy}>↻ <span>Refresh</span></button></header>
      {error && <div className="alert">{error}</div>}
      <section className="stats">
        <Stat title="Open reports" value={counts.open} hint="Awaiting review" icon="◷" tone="orange" />
        <Stat title="Actioned" value={counts.actioned} hint="Reports with action taken" icon="✓" tone="green" />
        <Stat title="Dismissed" value={counts.dismissed} hint="Reviewed and dismissed" icon="—" tone="blue" />
      </section>
      <section className="panel">
        <div className="panel-head"><div><h2>Report queue</h2><p>Review user reports and preserved message evidence.</p></div><select value={filter} onChange={(e) => { setFilter(e.target.value); setOffset(0); }}><option value="open">Open</option><option value="actioned">Actioned</option><option value="dismissed">Dismissed</option><option value="all">All reports</option></select></div>
        <div className="table-wrap"><table><thead><tr><th>REPORT</th><th>REASON</th><th>REPORTED USER</th><th>SUBMITTED</th><th>STATUS</th></tr></thead><tbody>
          {visibleReports.map((item) => <tr key={item.report_id} onClick={() => { setSelected(item); setNotes(item.resolution_notes || ''); }} className={selected?.report_id === item.report_id ? 'selected' : ''}>
            <td><strong>#{item.report_id.slice(0, 8)}</strong><small>Reporter {item.reporter_user_id.slice(0, 8)}</small></td>
            <td><span className={`reason reason-${item.reason}`}>{item.reason}</span></td>
            <td><code>{item.target_user_id.slice(0, 8)}…</code></td><td>{stamp(item.created_at)}</td><td><span className={`status status-${item.status}`}>{item.status}</span></td>
          </tr>)}
          {!visibleReports.length && <tr><td colSpan="5" className="empty">{busy ? 'Loading reports…' : 'No reports in this view.'}</td></tr>}
        </tbody></table></div>
        <div className="table-foot"><span>{resultCount ? `${offset + 1}–${Math.min(offset + reports.length, resultCount)} of ${resultCount} reports` : 'No reports'}</span><div className="pagination"><button className="secondary" disabled={offset === 0 || busy} onClick={() => setOffset(Math.max(0, offset - 100))}>Previous</button><button className="secondary" disabled={offset + reports.length >= resultCount || busy} onClick={() => setOffset(offset + 100)}>Next</button></div></div>
      </section>
      {selected && <div className="overlay" onClick={() => setSelected(null)}><section className="drawer" onClick={(e) => e.stopPropagation()}>
        <div className="drawer-head"><div><span className="eyebrow">REPORT REVIEW</span><h2>#{selected.report_id.slice(0, 8)}</h2></div><button className="close" onClick={() => setSelected(null)}>×</button></div>
        <div className="detail-row"><span>Status</span><span className={`status status-${selected.status}`}>{selected.status}</span></div>
        <div className="detail-row"><span>Reason</span><strong>{selected.reason}</strong></div>
        <div className="detail-row"><span>Submitted</span><strong>{stamp(selected.created_at)}</strong></div>
        <div className="detail-row"><span>Reporter</span><code>{selected.reporter_user_id}</code></div>
        <div className="detail-row"><span>Reported user</span><code>{selected.target_user_id}</code></div>
        {selected.conversation_id && <div className="detail-row"><span>Conversation</span><code>{selected.conversation_id}</code></div>}
        {selected.message_id && <div className="detail-row"><span>Message</span><code>{selected.message_id} · seq {selected.message_seq}</code></div>}
        {selected.details && <div className="evidence"><h3>Reporter notes</h3><p>{selected.details}</p></div>}
        {selected.message_snapshot && <div className="evidence"><h3>Preserved message</h3><pre>{JSON.stringify(selected.message_snapshot, null, 2)}</pre></div>}
        <label className="notes-label">Resolution notes<textarea rows="4" maxLength="2000" value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="Record what you reviewed or did…" /></label>
        {selected.reviewed_at && <p className="footnote">Reviewed {stamp(selected.reviewed_at)} · {selected.reviewed_by_moderator || selected.reviewed_by || 'Moderator'}</p>}
        <div className="actions"><button className="secondary" onClick={() => review('dismissed')} disabled={busy || selected.status === 'dismissed'}>Dismiss report</button><button className="primary" onClick={() => review('actioned')} disabled={busy || selected.status === 'actioned'}>Mark actioned</button></div>
      </section></div>}
    </main>
  </div>;
}

function Stat({ title, value, hint, icon, tone }) { return <div className="stat"><div className={`stat-icon ${tone}`}>{icon}</div><div><span>{title}</span><strong>{value}</strong><small>{hint}</small></div></div>; }

createRoot(document.getElementById('root')).render(<App />);
