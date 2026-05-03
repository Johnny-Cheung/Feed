import http from 'k6/http';
import { check, fail } from 'k6';

function formatFailure(res) {
  const parts = [`status=${res.status}`];
  if (res.error) {
    parts.push(`error=${res.error}`);
  }
  if (res.error_code) {
    parts.push(`error_code=${res.error_code}`);
  }
  if (res.body !== null && res.body !== undefined) {
    parts.push(`body=${res.body}`);
  }
  return parts.join(' ');
}

function safeParseJSON(res, name) {
  if (res.status === 0) {
    fail(`${name} request failed before receiving HTTP response: ${formatFailure(res)}`);
  }

  try {
    return res.json();
  } catch (err) {
    fail(`${name} returned non-JSON response: ${formatFailure(res)}`);
  }
}

export function jsonParams(token = '', tags = {}) {
  const headers = {
    'Content-Type': 'application/json',
  };
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }

  return {
    headers,
    tags,
  };
}

export function authParams(token = '', tags = {}) {
  const headers = {};
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }

  return {
    headers,
    tags,
  };
}

export function assertOK(res, name) {
  const body = safeParseJSON(res, name);
  const passed = check(res, {
    [`${name} status is 200`]: (response) => response.status === 200,
    [`${name} envelope code is 0`]: () => body && body.code === 0,
    [`${name} envelope message is ok`]: () => body && body.message === 'ok',
  });

  if (!passed) {
    fail(`${name} failed: ${formatFailure(res)}`);
  }

  return body.data;
}

export function assertStatus(res, name, expectedStatus) {
  const passed = check(res, {
    [`${name} status is ${expectedStatus}`]: (response) => response.status === expectedStatus,
  });

  if (!passed) {
    fail(
      `${name} failed: expected_status=${expectedStatus} actual_status=${res.status} ${formatFailure(res)}`,
    );
  }
}

export function login(baseUrl, username, password, label = 'login') {
  const res = http.post(
    `${baseUrl}/api/v1/auth/login`,
    JSON.stringify({ username, password }),
    jsonParams('', { endpoint: label }),
  );
  const data = assertOK(res, label);
  if (!data.access_token) {
    fail(`${label} did not return access_token`);
  }
  return data.access_token;
}

export function getCurrentUser(baseUrl, token, label = 'auth_me') {
  const res = http.get(
    `${baseUrl}/api/v1/auth/me`,
    authParams(token, { endpoint: label }),
  );
  return assertOK(res, label);
}
