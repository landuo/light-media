// The backend serves the same build at the LAN root and the fixed HTTPS proxy
// mount. Derive paths from this document, never from forwarded headers or input.
export function appBasePath(pathname = globalThis.location?.pathname || '/') {
  return pathname === '/light-media' || pathname.startsWith('/light-media/') ? '/light-media' : '';
}

export function apiURL(path: string) {
  if (!path.startsWith('/') || path.startsWith('//')) throw new Error('Invalid API path');
  return appBasePath() + '/api' + path;
}
