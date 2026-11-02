/* SignalDesk console: same-origin API, no frontend runtime dependencies. */
const $ = (selector, root = document) => root.querySelector(selector);
const state = { me: null, view: 'overview', workspace: null, registry: [], overview: null, offset: 0, total: 0, searchRange: null, timeline: null, loadGeneration: 0 };
const escapeHTML = value => String(value ?? '').replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char]));
const fmt = value => new Intl.NumberFormat('en-IE').format(value || 0);
const timestamp = value => value ? new Date(value).toISOString().replace('T', ' ').slice(0, 19) + ' UTC' : 'No indexed events';
const shortTime = value => new Date(value).toISOString().slice(11, 19);
const analyst = () => state.me?.role === 'analyst';
const range = () => { const to = new Date(); return { from: new Date(to - Number($('#time-range').value) * 3600000).toISOString(), to: to.toISOString() }; };
const params = object => new URLSearchParams(Object.entries(object).filter(([, value]) => value !== '' && value != null)).toString();
const empty = (title, detail = '') => `<div class="empty"><strong>${escapeHTML(title)}</strong>${escapeHTML(detail)}</div>`;
const severity = value => `<span class="badge ${escapeHTML(value)}">${escapeHTML(value)}</span>`;

async function api(path, options = {}) {
  const activeUser = state.me?.user_id;
  const response = await fetch('/api/v1' + path, { credentials: 'same-origin', ...options, headers: { 'Content-Type': 'application/json', ...options.headers } });
  const body = await response.json().catch(() => ({ error: 'Unexpected server response' }));
  if (activeUser && state.me?.user_id !== activeUser) throw new Error('Session changed. Refresh the current view.');
  if (!response.ok) { if (response.status === 401 && path !== '/auth/login') showLogin(); throw new Error(body.error || 'Request failed'); }
  return body;
}
function error(message) { $('#app-error').textContent = message; $('#app-error').hidden = false; }
function clearError() { $('#app-error').hidden = true; }
let toastTimer;
function toast(message) { $('#toast').textContent = message; $('#toast').hidden = false; clearTimeout(toastTimer); toastTimer = setTimeout(() => { $('#toast').hidden = true; }, 3500); }
function showLogin() { state.me = null; state.loadGeneration++; $('#app-view').hidden = true; $('#login-view').hidden = false; $('#timeline-dialog').close(); }
async function enter(me) {
  state.me = me;
  $('#login-view').hidden = true; $('#app-view').hidden = false;
  $('#tenant-name').textContent = me.tenant_name; $('#role-badge').textContent = me.role;
  $('#account-name').textContent = me.name; $('#account-email').textContent = me.email;
  $('#events-nav').hidden = !analyst(); $('#workspace-controls').hidden = !analyst();
  state.workspace = await api('/workspace');
  state.registry = (await api('/widgets')).widgets;
  $('#widget-type').innerHTML = state.registry.map(item => `<option value="${escapeHTML(item.type)}">${escapeHTML(item.title)}</option>`).join('');
  await navigate('overview');
}
$('#login-form').addEventListener('submit', async event => {
  event.preventDefault(); const button = $('button', event.currentTarget); button.disabled = true; $('#login-error').textContent = '';
  try { const me = await api('/auth/login', { method: 'POST', body: JSON.stringify({ email: $('#email').value, password: $('#password').value }) }); $('#password').value = ''; await enter(me); }
  catch (err) { if (state.me) error(err.message); else $('#login-error').textContent = err.message; }
  finally { button.disabled = false; }
});
async function signOut() { try { await api('/auth/logout', { method: 'POST' }); showLogin(); } catch (err) { error(err.message); } }
$('#logout').addEventListener('click', signOut);
$('#logout-mobile').addEventListener('click', signOut);
for (const button of document.querySelectorAll('[data-view]')) button.addEventListener('click', () => navigate(button.dataset.view));
async function navigate(view) {
  if (!state.me || (view === 'events' && !analyst())) return;
  clearError(); state.view = view;
  for (const name of ['overview', 'events', 'investigations']) $(`#${name}-view`).hidden = name !== view;
  for (const button of document.querySelectorAll('[data-view]')) button.classList.toggle('active', button.dataset.view === view);
  $('#section-label').textContent = view.toUpperCase();
  $('#page-title').textContent = { overview: 'Your security, connected.', events: 'Find the signal.', investigations: 'Keep the context.' }[view];
  await refresh();
}
async function refresh() {
  if (!state.me) return;
  clearError(); const button = $('#refresh'); button.disabled = true;
  try { if (state.view === 'overview') await overview(); else if (state.view === 'events') await searchEvents(); else await investigations(); }
  catch (err) { error(err.message); }
  finally { button.disabled = false; }
}
$('#refresh').addEventListener('click', refresh);
$('#time-range').addEventListener('change', () => { state.offset = 0; refresh(); });

async function overview() {
  const generation = ++state.loadGeneration;
  const data = await api('/overview?' + params(range()));
  if (generation !== state.loadGeneration || !state.me) return;
  state.overview = data;
  $('#freshness').textContent = 'LATEST INDEXED INGESTION / ' + timestamp(data.latest_indexed_ingestion);
  $('#metrics').innerHTML = [
    ['Indexed events', fmt(data.total), 'Across selected time range', ''],
    ['High-risk activity', fmt(data.high_risk), 'High + critical severity', 'risk'],
    ['Active entities', '≈ ' + fmt(data.entities), 'Approximate distinct count', ''],
    ['Security domains', String(data.domains.length).padStart(2, '0'), 'Identity / endpoint / cloud', '']
  ].map(([label, value, detail, cls]) => `<article class="metric-card ${cls}"><span class="metric-label">${label}</span><strong class="metric-value">${value}</strong><span class="metric-detail">${detail}</span></article>`).join('');
  await renderWidgets(generation);
}
function bars(buckets, preferred = []) {
  if (!buckets.length) return empty('Waiting for signals', 'Generate a scenario to populate the workspace.');
  const ordered = preferred.length ? [...buckets].sort((a, b) => preferred.indexOf(a.key) - preferred.indexOf(b.key)) : buckets;
  const max = Math.max(1, ...ordered.map(item => item.doc_count));
  return ordered.map(item => `<div class="bar-row"><span class="bar-label">${escapeHTML(item.key)}</span><progress value="${item.doc_count}" max="${max}" aria-label="${escapeHTML(item.key)}: ${item.doc_count}"></progress><span class="bar-count">${fmt(item.doc_count)}</span></div>`).join('');
}
function eventTable(events) {
  if (!events.length) return empty('No events in this view', 'Try a wider time range or generate sample activity.');
  return `<div class="table-wrap"><table><thead><tr><th>TIME / UTC</th><th>SEVERITY</th><th>DOMAIN</th><th>ENTITY</th><th>ACTIVITY</th></tr></thead><tbody>${events.map(event => `<tr><td class="mono">${shortTime(event.occurred_at)}</td><td>${severity(event.severity)}</td><td class="domain">${escapeHTML(event.domain)}</td><td><button class="entity-link" data-entity="${escapeHTML(event.entity_id)}">${escapeHTML(event.entity_id)}</button></td><td class="event-message" title="${escapeHTML(event.message)}">${escapeHTML(event.message || event.action)}</td></tr>`).join('')}</tbody></table></div>`;
}
async function renderWidgets(generation = state.loadGeneration) {
  const widgets = state.workspace.widgets;
  $('#widgets').innerHTML = widgets.length ? widgets.map((widget, index) => {
    const definition = state.registry.find(item => item.type === widget.type);
    const controls = analyst() ? `<div class="actions"><button class="icon-button" data-move="${index}" data-delta="-1" aria-label="Move widget up" ${index === 0 ? 'disabled' : ''}>↑</button><button class="icon-button" data-move="${index}" data-delta="1" aria-label="Move widget down" ${index === widgets.length - 1 ? 'disabled' : ''}>↓</button><button class="icon-button" data-remove="${index}" aria-label="Remove widget">×</button></div>` : '';
    return `<article class="panel ${['recent-events', 'entity-timeline'].includes(widget.type) ? 'wide' : ''}"><div class="panel-heading"><h2>${escapeHTML(definition?.title || widget.type)}</h2>${controls}</div><div class="widget-body" data-widget-index="${index}">${empty('Loading…')}</div></article>`;
  }).join('') : empty('Make room for your workflow', 'Add a widget to personalize your workspace.');
  await Promise.all(widgets.map(async (widget, index) => {
    let html;
    try {
      if (widget.type === 'severity-breakdown') html = bars(state.overview.severities, ['critical', 'high', 'medium', 'low', 'info']);
      else if (widget.type === 'domain-breakdown') html = bars(state.overview.domains);
      else if (widget.type === 'recent-events') { const data = await api('/events?' + params({ ...range(), domain: widget.config.domain || '', limit: 8 })); html = eventTable(data.events); }
      else { const data = await api('/timeline/' + encodeURIComponent(widget.config.entity_id) + '?' + params(range())); html = eventTable(data.events.slice(0, 8)); }
    } catch (err) { html = empty('Widget unavailable', err.message); }
    if (generation === state.loadGeneration) { const target = $(`[data-widget-index="${index}"]`); if (target) target.innerHTML = html; }
  }));
}
$('#widgets').addEventListener('click', async event => {
  const move = event.target.closest('[data-move]'); const remove = event.target.closest('[data-remove]');
  if (!move && !remove) return;
  if (move) { const from = Number(move.dataset.move), to = from + Number(move.dataset.delta); if (to < 0 || to >= state.workspace.widgets.length) return; [state.workspace.widgets[from], state.workspace.widgets[to]] = [state.workspace.widgets[to], state.workspace.widgets[from]]; }
  if (remove) state.workspace.widgets.splice(Number(remove.dataset.remove), 1);
  await renderWidgets(++state.loadGeneration);
});
$('#add-widget').addEventListener('click', () => { $('#widget-form').hidden = false; updateWidgetField(); });
$('#cancel-widget').addEventListener('click', () => { $('#widget-form').hidden = true; });
function updateWidgetField() { const type = $('#widget-type').value; $('#widget-config-label').hidden = !['entity-timeline', 'recent-events'].includes(type); $('#widget-config').placeholder = type === 'entity-timeline' ? 'e.g. alex.morgan' : 'Optional: identity, endpoint or cloud'; }
$('#widget-type').addEventListener('change', updateWidgetField);
$('#widget-form').addEventListener('submit', async event => {
  event.preventDefault(); if (state.workspace.widgets.length >= 8) { toast('A workspace supports up to eight widgets.'); return; }
  const type = $('#widget-type').value, value = $('#widget-config').value.trim(), config = {};
  if (type === 'entity-timeline') { if (!value) { toast('Enter an entity ID.'); return; } config.entity_id = value; }
  if (type === 'recent-events' && value) { if (!['identity', 'endpoint', 'cloud'].includes(value)) { toast('Choose identity, endpoint or cloud.'); return; } config.domain = value; }
  state.workspace.widgets.push({ id: crypto.randomUUID(), type, version: 1, config });
  $('#widget-form').hidden = true; $('#widget-config').value = ''; await renderWidgets(++state.loadGeneration); toast('Widget added. Save layout to keep changes.');
});
$('#save-workspace').addEventListener('click', async () => { try { await api('/workspace', { method: 'PUT', body: JSON.stringify(state.workspace) }); toast('Workspace saved.'); } catch (err) { error(err.message); } });

$('#search-form').addEventListener('submit', event => { event.preventDefault(); state.offset = 0; searchEvents().catch(err => error(err.message)); });
async function searchEvents(keepRange = false) {
  clearError(); if (!keepRange || !state.searchRange) state.searchRange = range();
  const data = await api('/events?' + params({ ...state.searchRange, q: $('#search-query').value, domain: $('#search-domain').value, severity: $('#search-severity').value, limit: 50, offset: state.offset }));
  state.total = data.total; $('#event-results').innerHTML = eventTable(data.events);
  $('#search-status').textContent = `${data.cached ? 'CACHED' : 'INDEX'} / ${timestamp(data.as_of)}`;
  $('#result-count').textContent = data.total ? `${state.offset + 1}–${state.offset + data.events.length} of ${fmt(data.total)} events` : '0 events';
  $('#previous-page').disabled = state.offset === 0; $('#next-page').disabled = state.offset + 50 >= Math.min(data.total, 1000);
}
$('#previous-page').addEventListener('click', () => { state.offset = Math.max(0, state.offset - 50); searchEvents(true).catch(err => error(err.message)); });
$('#next-page').addEventListener('click', () => { state.offset += 50; searchEvents(true).catch(err => error(err.message)); });
document.addEventListener('click', event => { const target = event.target.closest('[data-entity]'); if (target && analyst()) openTimeline(target.dataset.entity).catch(err => toast(err.message)); });

async function investigations() {
  const { investigations: items } = await api('/investigations');
  $('#investigation-list').innerHTML = items.length ? items.map(item => `<article class="investigation-card"><p class="eyebrow">INVESTIGATION / ${escapeHTML(item.id.slice(0, 8))}</p><h3>${escapeHTML(item.title)}</h3><span class="mono">ENTITY / ${escapeHTML(item.entity_id)}</span><span class="mono">SAVED / ${timestamp(item.created_at)}</span><div class="actions">${analyst() ? `<button class="button primary" data-investigation="${escapeHTML(item.id)}">Open timeline ↗</button><button class="button secondary" data-delete="${escapeHTML(item.id)}">Delete</button>` : '<span class="role-tag muted">READ ONLY</span>'}</div></article>`).join('') : empty('No saved investigations', analyst() ? 'Open an entity timeline and save the investigation.' : 'Your team’s saved investigations will appear here.');
  state.investigations = items;
}
$('#investigation-list').addEventListener('click', async event => {
  const open = event.target.closest('[data-investigation]'), remove = event.target.closest('[data-delete]');
  try {
    if (open) { const item = state.investigations.find(value => value.id === open.dataset.investigation); await openTimeline(item.entity_id, { from: item.from, to: item.to }); }
    if (remove && confirm('Delete this saved investigation?')) { await api('/investigations/' + encodeURIComponent(remove.dataset.delete), { method: 'DELETE' }); await investigations(); toast('Investigation deleted.'); }
  } catch (err) { toast(err.message); }
});
async function openTimeline(entity, bounds = range()) {
  const dialog = $('#timeline-dialog'); if (!dialog.open) dialog.showModal();
  $('#timeline-title').textContent = entity; $('#timeline-content').innerHTML = empty('Loading timeline…');
  const data = await api('/timeline/' + encodeURIComponent(entity) + '?' + params(bounds)); state.timeline = { entity, bounds, data };
  $('#timeline-content').innerHTML = `<p class="mono">${timestamp(bounds.from)} → ${timestamp(bounds.to)}</p><p class="mono">${data.events.length} events · most recent 200 maximum</p><form id="save-investigation-form" class="save-form"><label class="sr-only" for="investigation-title">Investigation title</label><input id="investigation-title" maxlength="160" value="${escapeHTML('Review: ' + entity)}" required><button class="button primary">Save investigation</button></form><section class="drawer-section"><h3>Signals to investigate</h3>${data.findings.length ? data.findings.map(item => `<div class="finding">${escapeHTML(item.title)}<div class="citations">${citations(item.evidence_ids)}</div></div>`).join('') : empty('No scenario indicators', 'Review the underlying activity below.')}</section><section class="drawer-section"><div class="brief-heading"><h3>Evidence brief</h3><button id="generate-brief" class="button secondary">Generate ↗</button></div><div id="brief-result"><p class="muted">Summarize this timeline with linked evidence.</p></div></section><section class="drawer-section"><h3>Activity timeline</h3><div class="timeline-list">${data.events.length ? [...data.events].reverse().map(item => `<article class="timeline-event" id="event-${escapeHTML(item.event_id)}"><div class="event-meta"><span class="mono">${shortTime(item.occurred_at)}</span><span class="domain">${escapeHTML(item.domain)}</span>${severity(item.severity)}</div><h3>${escapeHTML(item.action.replaceAll('_', ' '))}</h3><p>${escapeHTML(item.message)}</p><p class="mono">${escapeHTML(item.actor)} → ${escapeHTML(item.asset)}</p><span class="event-id">${escapeHTML(item.event_id)}</span></article>`).join('') : empty('No timeline events', 'The timeline projection may still be processing.')}</div></section>`;
}
function citations(ids) { return ids.map(id => `<button class="citation" data-evidence="${escapeHTML(id)}">${escapeHTML(id.slice(0, 8))} ↗</button>`).join(''); }
$('#close-timeline').addEventListener('click', () => $('#timeline-dialog').close());
$('#timeline-content').addEventListener('click', async event => {
  const citation = event.target.closest('[data-evidence]');
  if (citation) { const target = document.getElementById('event-' + citation.dataset.evidence); if (target) { target.scrollIntoView({ behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth', block: 'center' }); target.classList.add('highlight'); setTimeout(() => target.classList.remove('highlight'), 1800); } }
  if (event.target.closest('#generate-brief')) {
    const button = $('#generate-brief'); button.disabled = true; button.textContent = 'Generating…';
    try { const { brief } = await api('/briefs', { method: 'POST', body: JSON.stringify({ entity_id: state.timeline.entity, ...state.timeline.bounds }) }); $('#brief-result').innerHTML = `<p class="mono">${brief.method === 'bedrock' ? 'AMAZON BEDROCK' : 'DETERMINISTIC EVIDENCE BRIEF'}</p>${brief.claims.length ? brief.claims.map(claim => `<div class="brief-claim">${escapeHTML(claim.text)}<div class="citations">${citations(claim.evidence_ids)}</div></div>`).join('') : empty('No evidence to summarize')}`; }
    catch (err) { toast(err.message); }
    finally { button.disabled = false; button.textContent = 'Generate ↗'; }
  }
});
$('#timeline-content').addEventListener('submit', async event => {
  if (event.target.id !== 'save-investigation-form') return;
  event.preventDefault();
  try { await api('/investigations', { method: 'POST', body: JSON.stringify({ title: $('#investigation-title').value, entity_id: state.timeline.entity, ...state.timeline.bounds }) }); toast('Investigation saved for your team.'); }
  catch (err) { toast(err.message); }
});
api('/me').then(enter).catch(err => { if (state.me) error(err.message); else showLogin(); });
