import './design/worker-entry';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import './design/workspace.css';
import './features/settings/help.css';

// React owns only the application surface; DSM owns the surrounding desktop window.
createRoot(document.getElementById('root')!).render(<StrictMode><App /></StrictMode>);
