import './worker-entry';
import {StrictMode} from 'react';
import {createRoot} from 'react-dom/client';
import {DiskMapWorkspace} from './DiskMapWorkspace';
import {exampleVolumes} from './example-data';
import './workspace.css';

// This separate development entry cannot replace or authenticate the installed DSM application.
createRoot(document.getElementById('root')!).render(<StrictMode><DiskMapWorkspace volumes={exampleVolumes} initialId="media"/></StrictMode>);
