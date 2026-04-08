// ── WebSocket (native, replaces socket.io) ──────────────────────────────────
let ws = null;
let wsReady = false;
let wsReconnectTimer = null;

function wsConnect() {
  // Cancel any pending reconnect to avoid duplicate connections.
  if (wsReconnectTimer !== null) {
    clearTimeout(wsReconnectTimer);
    wsReconnectTimer = null;
  }

  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  ws = new WebSocket(`${proto}//${location.host}/ws`);

  window._wsRef = ws; // expose for inline scripts (custom mailbox modal)
  ws.onopen = () => {
    wsReady = true;
    const id = localStorage.getItem('mailboxId');
    if (id) {
      wsSend({ type: 'set_mailbox', id });
    } else {
      wsSend({ type: 'request_mailbox' });
    }
  };

  ws.onmessage = (e) => {
    let msg;
    try { msg = JSON.parse(e.data); } catch { return; }
    switch (msg.type) {
      case 'mailbox':
        setMailboxId(msg.id);
        fetchMailList();
        break;
      case 'mail':
        mailList.unshift(msg.mail);
        if (mailList.length > 50) mailList.length = 50;
        renderMailList();
        break;
      case 'mailbox_error':
        handleMailboxError(msg.code);
        break;
    }
  };

  ws.onclose = () => {
    wsReady = false;
    // Reconnect after 3s (only once; onerror calls close, so onclose handles both).
    wsReconnectTimer = setTimeout(wsConnect, 3000);
  };

  // onerror is always followed by onclose, so just close here and let onclose reconnect.
  ws.onerror = () => { ws.close(); };
}

function wsSend(obj) {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(obj));
  }
}

function handleMailboxError(code) {
  const customModal = document.getElementById('customModal');
  const customError = document.getElementById('customError');

  const previousId = localStorage.getItem('mailboxId');
  if (previousId) {
    currentMailboxId = previousId;
    const domain = typeof getEmailDomain === 'function' ? getEmailDomain() : location.hostname;
    mailboxInput.value = previousId + '@' + domain;
  }

  const msg = code === 'forbidden_prefix' ? '不允许使用该邮箱前缀' : '操作失败';

  if (customModal && !customModal.classList.contains('hidden') && customError) {
    customError.textContent = msg;
    customError.classList.remove('hidden');
  } else {
    showFloatingError(msg);
  }
}

wsConnect();

// ── State ───────────────────────────────────────────────────────────────────
let currentMailboxId = '';
let mailList = [];
let selectedMailIdx = null;

const mailboxInput = document.getElementById('mailboxId');
const copyBtn = document.getElementById('copyBtn');
const refreshBtn = document.getElementById('refreshBtn');
const customBtn = document.getElementById('customBtn');
const mailListTbody = document.getElementById('mailList');
const emptyTip = document.getElementById('emptyTip');

const modal = document.getElementById('modal');
const closeModal = document.getElementById('closeModal');
const modalSubject = document.getElementById('modalSubject');
const modalFrom = document.getElementById('modalFrom');
const modalDate = document.getElementById('modalDate');
const modalContent = document.getElementById('modalContent');
const showRaw = document.getElementById('showRaw');
const rawContent = document.getElementById('rawContent');
const darkToggle = document.getElementById('darkToggle');

// 复制功能
new ClipboardJS('#copyBtn');

function setMailboxId(id) {
  currentMailboxId = id;
  const domain = typeof getEmailDomain === 'function' ? getEmailDomain() : location.hostname;
  mailboxInput.value = id + '@' + domain;
  localStorage.setItem('mailboxId', id);
}

// Escape a string for safe insertion into HTML text content via innerHTML.
function escHtml(s) {
  const d = document.createElement('div');
  d.textContent = s == null ? '' : String(s);
  return d.innerHTML;
}

function renderMailList() {
  mailListTbody.innerHTML = '';
  const mailCards = document.getElementById('mailCards');
  mailCards.innerHTML = '';
  if (mailList.length === 0) {
    emptyTip.classList.remove('hidden');
    return;
  }
  emptyTip.classList.add('hidden');

  mailList.forEach((mail, idx) => {
    // 桌面端表格行
    const tr = document.createElement('tr');
    tr.className = `border-b transition cursor-pointer ${idx === selectedMailIdx ? 'selected-row' : ''}`;
    tr.style.borderColor = 'var(--border-color)';
    tr.onmouseover = function() { this.style.backgroundColor = 'rgba(79, 70, 229, 0.1)'; };
    tr.onmouseout  = function() { this.style.backgroundColor = ''; };
    tr.innerHTML = `
      <td class="px-4 py-3 whitespace-nowrap overflow-hidden text-ellipsis" style="color:var(--text-primary);">${escHtml(mail.from)}</td>
      <td class="px-4 py-3 whitespace-nowrap overflow-hidden text-ellipsis" style="color:var(--text-primary);">${escHtml(mail.subject)}</td>
      <td class="px-4 py-3 whitespace-nowrap" style="color:var(--text-tertiary);">${mail.date ? escHtml(new Date(mail.date).toLocaleString()) : ''}</td>
      <td class="px-4 py-3 text-right">
        <button class="delete-btn p-1.5 rounded-lg transition-colors" title="删除邮件" data-idx="${idx}" style="background-color:rgba(79,70,229,.1);color:#ef4444;">
          <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/></svg>
        </button>
      </td>`;
    Array.from(tr.children).slice(0, 3).forEach(td => {
      td.addEventListener('click', () => { selectedMailIdx = idx; renderMailList(); showMailDetail(mail); });
    });
    tr.querySelector('.delete-btn').onclick = e => { e.stopPropagation(); deleteMail(idx); };
    mailListTbody.appendChild(tr);

    // 移动端卡片
    const card = document.createElement('div');
    card.className = `rounded-lg px-4 py-3 ${idx === selectedMailIdx ? 'selected-card' : ''}`;
    card.style.cssText = 'background-color:rgba(79,70,229,.05);border:1px solid var(--border-color)';
    card.innerHTML = `
      <div class="flex items-center justify-between mb-1">
        <span class="font-medium text-base truncate max-w-[60%]" style="color:var(--text-primary);">${escHtml(mail.from)}</span>
        <span class="text-xs ml-2" style="color:var(--text-tertiary);">${mail.date ? escHtml(new Date(mail.date).toLocaleString()) : ''}</span>
      </div>
      <div class="text-sm font-medium truncate mb-2" style="color:var(--text-primary);">${escHtml(mail.subject)}</div>
      <div class="flex justify-end">
        <button class="delete-btn p-1.5 rounded-lg transition-colors" title="删除邮件" data-idx="${idx}" style="background-color:rgba(79,70,229,.1);color:#ef4444;">
          <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/></svg>
        </button>
      </div>`;
    card.querySelectorAll('span,div.text-sm').forEach(el => {
      el.addEventListener('click', () => { selectedMailIdx = idx; renderMailList(); showMailDetail(mail); });
    });
    card.querySelector('.delete-btn').onclick = e => { e.stopPropagation(); deleteMail(idx); };
    mailCards.appendChild(card);
  });
}

function fetchMailList() {
  if (!currentMailboxId) return;
  const domain = typeof getEmailDomain === 'function' ? getEmailDomain() : location.hostname;
  fetch(`/api/mails/${encodeURIComponent(currentMailboxId + '@' + domain)}`)
    .then(r => r.json())
    .then(d => { mailList = d.mails || []; renderMailList(); });
}

function deleteMail(idx) {
  if (!currentMailboxId) return;
  const domain = typeof getEmailDomain === 'function' ? getEmailDomain() : location.hostname;
  fetch(`/api/mails/${encodeURIComponent(currentMailboxId + '@' + domain)}/${idx}`, { method: 'DELETE' })
    .then(r => r.json())
    .then(d => { if (d.success) fetchMailList(); });
}

function showMailDetail(mail) {
  modalSubject.textContent = mail.subject || '(无主题)';
  modalFrom.innerHTML = `<span class='font-semibold' style='color:var(--text-secondary);'>发件人：</span><span style='color:var(--text-secondary);'>${escHtml(mail.from)}</span>`;
  modalDate.textContent = mail.date ? new Date(mail.date).toLocaleString() : '';
  if (mail.html) {
    modalContent.innerHTML = mail.html;
  } else {
    modalContent.textContent = mail.text || '';
  }
  if (mail.attachments && mail.attachments.length > 0) {
    const attDiv = document.createElement('div');
    attDiv.className = 'mt-4 flex flex-wrap gap-2';
    attDiv.innerHTML = mail.attachments.map(a =>
      `<span class='inline-flex items-center px-2 py-1 rounded text-xs' style='background-color:rgba(79,70,229,.1);color:var(--text-secondary);'>
        <svg class="w-4 h-4 mr-1" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15.172 7l-6.586 6.586a2 2 0 102.828 2.828l6.414-6.586a4 4 0 00-5.656-5.656l-6.415 6.585a6 6 0 108.486 8.486L20.5 13"/></svg>
        ${escHtml(a.filename || '附件')}
      </span>`
    ).join('');
    modalContent.appendChild(attDiv);
  }
  rawContent.textContent = '';
  rawContent.classList.add('hidden');
  modal.classList.remove('hidden');
}

closeModal.onclick = () => modal.classList.add('hidden');
showRaw.onclick    = () => rawContent.classList.toggle('hidden');

refreshBtn.onclick = () => {
  wsSend({ type: 'request_mailbox' });
};

// 显示浮动错误提示
function showFloatingError(message) {
  let el = document.getElementById('floating-error');
  if (!el) {
    el = document.createElement('div');
    el.id = 'floating-error';
    el.className = 'fixed top-24 right-10 z-50 bg-red-500 text-white py-2 px-4 rounded-lg shadow-lg transition-opacity duration-300';
    el.style.opacity = '0';
    document.body.appendChild(el);
  }
  el.textContent = message;
  setTimeout(() => { el.style.opacity = '1'; }, 10);
  setTimeout(() => {
    el.style.opacity = '0';
    setTimeout(() => el.parentNode && el.parentNode.removeChild(el), 300);
  }, 3000);
}

// 定时轮询兜底（10s），防止 WS 漏推
setInterval(fetchMailList, 10000);


// 主题切换（三态：auto → dark → light → auto）
if (darkToggle) {
  const ICONS = {
    dark: '<path d="M17.293 13.293A8 8 0 016.707 2.707a8.001 8.001 0 1010.586 10.586z"/>',
    light: '<path fill-rule="evenodd" d="M10 2a1 1 0 011 1v1a1 1 0 11-2 0V3a1 1 0 011-1zm4 8a4 4 0 11-8 0 4 4 0 018 0zm-.464 4.95l.707.707a1 1 0 001.414-1.414l-.707-.707a1 1 0 00-1.414 1.414zm2.12-10.607a1 1 0 010 1.414l-.706.707a1 1 0 11-1.414-1.414l.707-.707a1 1 0 011.414 0zM17 11a1 1 0 100-2h-1a1 1 0 100 2h1zm-7 4a1 1 0 011 1v1a1 1 0 11-2 0v-1a1 1 0 011-1zM5.05 6.464A1 1 0 106.465 5.05l-.708-.707a1 1 0 00-1.414 1.414l.707.707zm1.414 8.486l-.707.707a1 1 0 01-1.414-1.414l.707-.707a1 1 0 011.414 1.414zM4 11a1 1 0 100-2H3a1 1 0 000 2h1z" clip-rule="evenodd"/>',
    auto: '<path d="M10 2a8 8 0 100 16A8 8 0 0010 2zm0 2v12a6 6 0 000-12z"/>',
  };
  const TITLES = { dark: '暗黑模式', light: '明亮模式', auto: '跟随系统' };
  const CYCLE = ['auto', 'dark', 'light'];

  function applyTheme(mode) {
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const isDark = mode === 'dark' || (mode === 'auto' && prefersDark);
    document.documentElement.classList.toggle('dark', isDark);
    const icon = document.getElementById('themeIcon');
    if (icon) icon.innerHTML = ICONS[mode];
    darkToggle.setAttribute('title', TITLES[mode]);
    darkToggle.setAttribute('aria-label', TITLES[mode]);
  }

  const saved = localStorage.getItem('pctag:theme') || 'auto';
  applyTheme(saved);

  darkToggle.addEventListener('click', () => {
    const cur = localStorage.getItem('pctag:theme') || 'auto';
    const next = CYCLE[(CYCLE.indexOf(cur) + 1) % CYCLE.length];
    localStorage.setItem('pctag:theme', next);
    applyTheme(next);
  });
}

// 暴露给 index.html 内联脚本（自定义邮箱弹窗）
window.wsSend = wsSend;
