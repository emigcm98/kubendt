import React from 'react';
import { useNavigate } from 'react-router-dom';
import AppHeader from '../components/AppHeader';
import { ReactComponent as SearchIcon } from '../assets/images/icons/search.svg';
import { ReactComponent as LockIcon } from '../assets/images/icons/lock.svg';
import { ReactComponent as WarningIcon } from '../assets/images/icons/warning.svg';
import './ErrorPage.css';

const TITLES = {
  403: 'No access',
  404: 'Nothing here',
  500: 'Something failed',
  503: 'No cluster yet',
};

const ICONS = { 403: LockIcon, 404: SearchIcon };

// The message as the backend gave it, its JSON error when it is one.
const messageOf = (raw) => {
  try {
    const parsed = JSON.parse(raw);
    if (parsed.error) return parsed.error;
  } catch {
    // not JSON, shown as it came
  }
  return raw;
};

const sentence = (s) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s);

// A page that could not be shown: a route that does not exist, a namespace
// one may not open, a backend that failed. The bar stays, so it is never a
// dead end.
function ErrorPage({ statusCode = 500, rawMessage = 'Something went wrong on the way.' }) {
  const navigate = useNavigate();
  const Icon = ICONS[statusCode] || WarningIcon;
  return (
    <div className="error-page">
      <AppHeader />
      <main className="error-main">
        <section className={`error-card error-card-${statusCode >= 500 ? 'fail' : 'refused'}`}>
          <span className="error-code">{statusCode}</span>
          <h1>
            <Icon className="app-icon" aria-hidden="true" />
            {TITLES[statusCode] || 'Something went wrong'}
          </h1>
          <p>{sentence(messageOf(rawMessage))}</p>
          <div className="error-actions">
            <button type="button" className="error-btn-quiet" onClick={() => navigate(-1)}>
              Back
            </button>
            <button type="button" className="error-btn" onClick={() => navigate('/')}>
              Go to the main page
            </button>
          </div>
        </section>
      </main>
    </div>
  );
}

export default ErrorPage;
