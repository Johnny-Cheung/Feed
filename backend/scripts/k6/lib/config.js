import exec from 'k6/execution';
import { SharedArray } from 'k6/data';

function parsePositiveInt(raw, fallback) {
  const parsed = Number.parseInt(raw || '', 10);
  if (Number.isNaN(parsed) || parsed <= 0) {
    return fallback;
  }
  return parsed;
}

function parseNonNegativeFloat(raw, fallback) {
  if (raw === undefined || raw === null || raw === '') {
    return fallback;
  }

  const parsed = Number.parseFloat(raw);
  if (Number.isNaN(parsed) || parsed < 0) {
    return fallback;
  }
  return parsed;
}

function parseIdList(raw) {
  if (!raw) {
    return [];
  }

  return raw
    .split(',')
    .map((item) => Number.parseInt(item.trim(), 10))
    .filter((item) => Number.isInteger(item) && item > 0);
}

function loadSeedData() {
  const seedPath = __ENV.SEED_DATA_PATH || '';
  if (!seedPath) {
    return null;
  }

  const seedItems = new SharedArray('seed-data', () => {
    const payload = JSON.parse(open(seedPath));
    return [payload];
  });

  return seedItems[0];
}

function deriveVideoIds(seedData) {
  const envVideoIds = parseIdList(__ENV.VIDEO_IDS || __ENV.VIDEO_ID || '');
  if (envVideoIds.length > 0) {
    return envVideoIds;
  }
  if (seedData && Array.isArray(seedData.video_ids)) {
    return seedData.video_ids.filter((item) => Number.isInteger(item) && item > 0);
  }
  return [];
}

function deriveAuthorIds(seedData) {
  const envAuthorIds = parseIdList(__ENV.AUTHOR_IDS || __ENV.AUTHOR_ID || '');
  if (envAuthorIds.length > 0) {
    return envAuthorIds;
  }
  if (seedData && Array.isArray(seedData.authors)) {
    return seedData.authors
      .map((item) => item.id)
      .filter((item) => Number.isInteger(item) && item > 0);
  }
  return [];
}

function deriveViewerUsers(seedData) {
  if (seedData && Array.isArray(seedData.viewers) && seedData.viewers.length > 0) {
    return seedData.viewers
      .filter((item) => item && item.username && item.password)
      .map((item) => ({
        username: String(item.username),
        password: String(item.password),
        id: Number.isInteger(item.id) ? item.id : 0,
      }));
  }

  return [
    {
      username: __ENV.VIEWER_USERNAME || 'viewer001',
      password: __ENV.VIEWER_PASSWORD || '123456',
      id: 0,
    },
  ];
}

const seedData = loadSeedData();

export const config = {
  baseUrl: __ENV.BASE_URL || 'http://localhost:18080',
  seedDataPath: __ENV.SEED_DATA_PATH || '',
  authorUsername: __ENV.AUTHOR_USERNAME || 'author001',
  authorPassword: __ENV.AUTHOR_PASSWORD || '1234567',
  viewerUsername: __ENV.VIEWER_USERNAME || 'viewer001',
  viewerPassword: __ENV.VIEWER_PASSWORD || '123456',
  authorId: parsePositiveInt(__ENV.AUTHOR_ID, 0),
  authorIds: deriveAuthorIds(seedData),
  viewerUsers: deriveViewerUsers(seedData),
  videoIds: deriveVideoIds(seedData),
  pauseSeconds: parseNonNegativeFloat(__ENV.PAUSE_SECONDS, 0.3),
};

export function uniqueSuffix(prefix = 'k6') {
  return `${prefix}-${exec.vu.idInTest}-${exec.scenario.iterationInTest}-${Date.now()}`;
}

export function pickOne(items) {
  if (!items || items.length === 0) {
    return 0;
  }
  return items[Math.floor(Math.random() * items.length)];
}

export function requireVideoIds(videoIds) {
  if (videoIds && videoIds.length > 0) {
    return videoIds;
  }

  throw new Error(
    'No video IDs configured. Set VIDEO_IDS=1,2,3 or VIDEO_ID=1 before running k6.',
  );
}
