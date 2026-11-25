// App.js
import React from 'react';
import { Routes, Route } from 'react-router-dom';
import Home from './pages/Home';
import ClusterPage from './pages/ClusterPage';
import ProfilePage from './pages/ProfilePage';
import SettingsPage from './pages/SettingsPage';
import DriversPage from './pages/DriversPage';
import NamespacePage from './pages/NamespacePage';
import NamespaceFilesPage from './pages/NamespaceFilesPage';
import StatePage from './pages/StatePage';
import UsersPage from './pages/UsersPage';
import ErrorPage from './pages/ErrorPage';

function App() {
  return (
    <Routes>
      <Route path="/" element={<Home />} />
      <Route path="/cluster" element={<ClusterPage />} />
      <Route path="/profile" element={<ProfilePage />} />
      <Route path="/settings" element={<SettingsPage />} />
      <Route path="/drivers/:driver?" element={<DriversPage />} />
      <Route path="/state" element={<StatePage />} />
      <Route path="/users" element={<UsersPage />} />
      <Route path="/:namespace/files" element={<NamespaceFilesPage />} />
      <Route path="/:namespace/state" element={<StatePage />} />
      <Route path="/:namespace" element={<NamespacePage />} />
      <Route path="*" element={<ErrorPage statusCode={404} rawMessage="Route not found" />} />
    </Routes>
  );
}

export default App;
