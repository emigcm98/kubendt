// index.js
import React from 'react';
import ReactDOM from 'react-dom/client';
import './index.css';
import App from './App';
import { BrowserRouter } from 'react-router-dom';
import AuthGate from './components/AuthGate';
import { installApiClient } from './apiClient';

installApiClient();

const root = ReactDOM.createRoot(document.getElementById('root'));
root.render(
  <BrowserRouter>
    <AuthGate>
      <App />
    </AuthGate>
  </BrowserRouter>
);
