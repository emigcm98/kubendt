// App.js
import React from 'react';
import { Routes, Route } from 'react-router-dom';
import Home from './pages/Home';
import NamespacePage from './pages/NamespacePage';
import NamespaceFilesPage from './pages/NamespaceFilesPage';
import ErrorPage from './pages/ErrorPage';

function App() {
  return (
    <Routes>
      <Route path="/" element={<Home />} />
      <Route path="/:namespace/files" element={<NamespaceFilesPage />} />
      <Route path="/:namespace" element={<NamespacePage />} />
      <Route path="*" element={<ErrorPage statusCode={404} rawMessage="Route not found" />} />
    </Routes>
  );
}

export default App;
