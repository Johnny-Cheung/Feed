import http from 'k6/http';
import { sleep } from 'k6';

import { assertOK, authParams, login, getCurrentUser } from './lib/api.js';
import { config, pickOne, requireVideoIds } from './lib/config.js';

export const options = {
  vus: Number(__ENV.READ_VUS || 20),
  duration: __ENV.READ_DURATION || '2m',
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<500'],
  },
};

export function setup() {
  const viewerSessions = config.viewerUsers.map((viewerUser, index) => {
    const labelPrefix = `viewer_${index + 1}`;
    const token = login(
      config.baseUrl,
      viewerUser.username,
      viewerUser.password,
      `${labelPrefix}_login`,
    );
    const viewer = getCurrentUser(config.baseUrl, token, `${labelPrefix}_me`);

    return {
      username: viewerUser.username,
      token,
      id: viewer.id,
    };
  });

  return {
    viewerSessions,
    videoIds: requireVideoIds(config.videoIds),
  };
}

export default function (data) {
  const viewer = pickOne(data.viewerSessions);
  const roll = Math.random();

  if (roll < 0.2) {
    assertOK(
      http.get(`${config.baseUrl}/api/v1/feed/home?limit=10`, { tags: { endpoint: 'feed_home_anon' } }),
      'feed_home_anon',
    );
  } else if (roll < 0.7) {
    assertOK(
      http.get(
        `${config.baseUrl}/api/v1/feed/home?limit=10`,
        authParams(viewer.token, { endpoint: 'feed_home_auth' }),
      ),
      'feed_home_auth',
    );
  } else if (roll < 0.85) {
    const videoId = pickOne(data.videoIds);
    assertOK(
      http.get(
        `${config.baseUrl}/api/v1/videos/${videoId}`,
        authParams(viewer.token, { endpoint: 'video_detail' }),
      ),
      'video_detail',
    );
  } else {
    assertOK(
      http.get(
        `${config.baseUrl}/api/v1/feed/following?limit=10`,
        authParams(viewer.token, { endpoint: 'feed_following' }),
      ),
      'feed_following',
    );
  }

  sleep(config.pauseSeconds);
}
