export function mount() {
  const host = document.createElement('div');
  host.id = 'tafakor-ai-host';
  host.setAttribute('data-ai-ignore', '');
  document.body.appendChild(host);
  host.attachShadow({ mode: 'open' });
}
