// Tafakor AI widget entry: mounts the assistant in a Shadow DOM so it can never
// clash with the application's styles or scripts.
import { mount } from './app';

if (!document.getElementById('tafakor-ai-host')) {
  const start = () => mount();
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
  else start();
}
