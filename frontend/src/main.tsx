import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import './styles/tokens.css';
import './styles/base.css';
import './styles/components.css';
import './styles/layout.css';
import { App } from './App';
import { clearReloadFlag, reloadForUpdate } from './components/ErrorBoundary';

// A stale tab after a new deployment cannot load renamed page chunks: reload once to get the new version.
window.addEventListener('vite:preloadError', (event) => {
  if (reloadForUpdate()) event.preventDefault();
});
// The page loaded fine, so allow a future update reload.
window.addEventListener('load', () => setTimeout(clearReloadFlag, 10_000));

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
