import http from 'k6/http';
import { sleep } from 'k6';

import { assertOK, authParams, jsonParams, login, getCurrentUser } from './lib/api.js';
import { config, pickOne, requireVideoIds, uniqueSuffix } from './lib/config.js';

export const options = {
  vus: Number(__ENV.SOAK_VUS || 30),
  duration: __ENV.SOAK_DURATION || '10m',
  thresholds: {
    http_req_failed: ['rate<0.02'],
    http_req_duration: ['p(95)<300'],
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

  let authorIds = config.authorIds.slice();
  if (authorIds.length === 0) {
    const authorToken = login(config.baseUrl, config.authorUsername, config.authorPassword, 'author_login');
    const author = getCurrentUser(config.baseUrl, authorToken, 'author_me');
    authorIds = [config.authorId || author.id];
  }

  return {
    viewerSessions,
    authorIds,
    videoIds: requireVideoIds(config.videoIds),
  };
}

function doRead(viewerToken, videoIds) {
  const readRoll = Math.random();

  if (readRoll < 0.2) {
    assertOK(
      http.get(`${config.baseUrl}/api/v1/feed/home?limit=10`, { tags: { endpoint: 'feed_home_anon' } }),
      'feed_home_anon',
    );
  } else if (readRoll < 0.7) {
    assertOK(
      http.get(
        `${config.baseUrl}/api/v1/feed/home?limit=10`,
        authParams(viewerToken, { endpoint: 'feed_home_auth' }),
      ),
      'feed_home_auth',
    );
  } else if (readRoll < 0.85) {
    const videoId = pickOne(videoIds);
    assertOK(
      http.get(
        `${config.baseUrl}/api/v1/videos/${videoId}`,
        authParams(viewerToken, { endpoint: 'video_detail' }),
      ),
      'video_detail',
    );
  } else {
    assertOK(
      http.get(
        `${config.baseUrl}/api/v1/feed/following?limit=10`,
        authParams(viewerToken, { endpoint: 'feed_following' }),
      ),
      'feed_following',
    );
  }
}

function doWrite(viewer, authorIds, videoIds) {
  const authorId = pickOne(authorIds);
  const videoId = pickOne(videoIds);
  const writeRoll = Math.random();

  if (writeRoll < 0.2) {
    assertOK(
      http.post(
        `${config.baseUrl}/api/v1/videos/${videoId}/likes`,
        null,
        authParams(viewer.token, { endpoint: 'like_video' }),
      ),
      'like_video',
    );
  } else if (writeRoll < 0.4) {
    assertOK(
      http.del(
        `${config.baseUrl}/api/v1/videos/${videoId}/likes`,
        null,
        authParams(viewer.token, { endpoint: 'unlike_video' }),
      ),
      'unlike_video',
    );
  } else if (writeRoll < 0.55) {
    assertOK(
      http.post(
        `${config.baseUrl}/api/v1/videos/${videoId}/favorites`,
        null,
        authParams(viewer.token, { endpoint: 'favorite_video' }),
      ),
      'favorite_video',
    );
  } else if (writeRoll < 0.7) {
    assertOK(
      http.del(
        `${config.baseUrl}/api/v1/videos/${videoId}/favorites`,
        null,
        authParams(viewer.token, { endpoint: 'unfavorite_video' }),
      ),
      'unfavorite_video',
    );
  } else if (writeRoll < 0.9) {
    const comment = assertOK(
      http.post(
        `${config.baseUrl}/api/v1/videos/${videoId}/comments`,
        JSON.stringify({ content: uniqueSuffix('soak-comment') }),
        jsonParams(viewer.token, { endpoint: 'create_comment' }),
      ),
      'create_comment',
    );

    if (comment.id) {
      assertOK(
        http.del(
          `${config.baseUrl}/api/v1/comments/${comment.id}`,
          null,
          authParams(viewer.token, { endpoint: 'delete_comment' }),
        ),
        'delete_comment',
      );
    }
  } else if (viewer.id !== authorId) {
    if (Math.random() < 0.5) {
      assertOK(
        http.post(
          `${config.baseUrl}/api/v1/users/${authorId}/follow`,
          null,
          authParams(viewer.token, { endpoint: 'follow_user' }),
        ),
        'follow_user',
      );
    } else {
      assertOK(
        http.del(
          `${config.baseUrl}/api/v1/users/${authorId}/follow`,
          null,
          authParams(viewer.token, { endpoint: 'unfollow_user' }),
        ),
        'unfollow_user',
      );
    }
  }
}

export default function (data) {
  const viewer = pickOne(data.viewerSessions);
  const roll = Math.random();

  if (roll < 0.7) {
    doRead(viewer.token, data.videoIds);
  } else {
    doWrite(viewer, data.authorIds, data.videoIds);
  }

  sleep(config.pauseSeconds);
}
