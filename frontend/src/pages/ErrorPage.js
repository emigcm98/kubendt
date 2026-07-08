// src/components/ErrorPage.jsx
import React from 'react';
import { useNavigate } from 'react-router-dom';
import './ErrorPage.css';

const statusTitles = {
  403: '403 Forbidden',
  404: '404 Not Found',
  500: '500 Internal Server Error',
};

function ErrorPage({ statusCode = 500, rawMessage = 'An unexpected error occurred' }) {
  const navigate = useNavigate();

  let cleanMessage = rawMessage;
  try {
    const parsed = JSON.parse(rawMessage);
    if (parsed.error) cleanMessage = parsed.error;
  } catch {
    // no-op
  }

  const title = statusTitles[statusCode] || `Error ${statusCode}`;

  return (
    <div className="error-page-container">
      <div className="error-card">
        <div className="error-icon">🚫</div>
        <h1 className="error-title">{title}</h1>
        <p className="error-message">{cleanMessage}</p>
        <button className="error-button" onClick={() => navigate('/')}>
          ← Back to Home
        </button>
      </div>
    </div>
  );
}

export default ErrorPage;
