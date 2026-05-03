import http from 'k6/http';
import { check, fail, group, sleep } from 'k6';

import { assertOK, authParams, jsonParams, login, getCurrentUser } from './lib/api.js';
import { config, requireVideoIds, uniqueSuffix } from './lib/config.js';

export const options = {
  vus: Number(__ENV.SMOKE_VUS || 1),
  iterations: Number(__ENV.SMOKE_ITERATIONS || 1),
  thresholds: {
    http_req_failed: ['rate==0'],
    http_req_duration: ['p(95)<1500'],
  },
};

export function setup() {
  const authorToken = login(config.baseUrl, config.authorUsername, config.authorPassword, 'author_login');
  const viewerToken = login(config.baseUrl, config.viewerUsername, config.viewerPassword, 'viewer_login');
  const author = getCurrentUser(config.baseUrl, authorToken, 'author_me');
  const viewer = getCurrentUser(config.baseUrl, viewerToken, 'viewer_me');

  return {
    authorToken,
    viewerToken,
    authorId: config.authorId || author.id,
    viewerId: viewer.id,
    videoIds: config.videoIds,
  };
}

export default function (data) {
  group('system', () => {
    assertOK(http.get(`${config.baseUrl}/ping`, { tags: { endpoint: 'ping' } }), 'ping');
    const health = assertOK(http.get(`${config.baseUrl}/health`, { tags: { endpoint: 'health' } }), 'health');

    const healthPassed = check(health, {
      'health mysql ok': (payload) => payload.mysql === 'ok',
      'health redis ok': (payload) => payload.redis === 'ok',
      'health rabbitmq ok': (payload) => payload.rabbitmq === 'ok',
    });
    if (!healthPassed) {
      fail(`health dependencies not ready: ${JSON.stringify(health)}`);
    }
  });

  const videoIds = requireVideoIds(data.videoIds);
  const videoId = videoIds[0];

  group('core reads', () => {
    assertOK(
      http.get(`${config.baseUrl}/api/v1/feed/home?limit=10`, { tags: { endpoint: 'feed_home_anon' } }),
      'feed_home_anon',
    );
    assertOK(
      http.get(
        `${config.baseUrl}/api/v1/feed/home?limit=10`,
        authParams(data.viewerToken, { endpoint: 'feed_home_auth' }),
      ),
      'feed_home_auth',
    );
    assertOK(
      http.get(
        `${config.baseUrl}/api/v1/feed/following?limit=10`,
        authParams(data.viewerToken, { endpoint: 'feed_following' }),
      ),
      'feed_following',
    );

    const detail = assertOK(
      http.get(
        `${config.baseUrl}/api/v1/videos/${videoId}`,
        authParams(data.viewerToken, { endpoint: 'video_detail' }),
      ),
      'video_detail',
    );

    const detailPassed = check(detail, {
      'video detail has matching id': (payload) => payload.id === videoId,
      'video detail has author': (payload) => payload.author && payload.author.id > 0,
      'video detail has stats': (payload) => payload.stats !== undefined,
      'video detail has viewer_state': (payload) => payload.viewer_state !== undefined,
    });
    if (!detailPassed) {
      fail(`video detail malformed: ${JSON.stringify(detail)}`);
    }
  });

  group('interaction roundtrip', () => {
    assertOK(
      http.post(
        `${config.baseUrl}/api/v1/videos/${videoId}/likes`,
        null,
        authParams(data.viewerToken, { endpoint: 'like_video' }),
      ),
      'like_video',
    );
    assertOK(
      http.del(
        `${config.baseUrl}/api/v1/videos/${videoId}/likes`,
        null,
        authParams(data.viewerToken, { endpoint: 'unlike_video' }),
      ),
      'unlike_video',
    );

    assertOK(
      http.post(
        `${config.baseUrl}/api/v1/videos/${videoId}/favorites`,
        null,
        authParams(data.viewerToken, { endpoint: 'favorite_video' }),
      ),
      'favorite_video',
    );
    assertOK(
      http.del(
        `${config.baseUrl}/api/v1/videos/${videoId}/favorites`,
        null,
        authParams(data.viewerToken, { endpoint: 'unfavorite_video' }),
      ),
      'unfavorite_video',
    );

    const comment = assertOK(
      http.post(
        `${config.baseUrl}/api/v1/videos/${videoId}/comments`,
        JSON.stringify({ content: uniqueSuffix('smoke-comment') }),
        jsonParams(data.viewerToken, { endpoint: 'create_comment' }),
      ),
      'create_comment',
    );
    if (!comment.id) {
      fail(`create_comment did not return comment id: ${JSON.stringify(comment)}`);
    }
    assertOK(
      http.del(
        `${config.baseUrl}/api/v1/comments/${comment.id}`,
        null,
        authParams(data.viewerToken, { endpoint: 'delete_comment' }),
      ),
      'delete_comment',
    );

    if (data.viewerId !== data.authorId) {
      assertOK(
        http.post(
          `${config.baseUrl}/api/v1/users/${data.authorId}/follow`,
          null,
          authParams(data.viewerToken, { endpoint: 'follow_user' }),
        ),
        'follow_user',
      );
      assertOK(
        http.del(
          `${config.baseUrl}/api/v1/users/${data.authorId}/follow`,
          null,
          authParams(data.viewerToken, { endpoint: 'unfollow_user' }),
        ),
        'unfollow_user',
      );
    }
  });

  sleep(config.pauseSeconds);
}

