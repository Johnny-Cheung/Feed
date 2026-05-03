package hotscore

import "time"

// Calculate returns the V1 hot score for a video.
func Calculate(publishedAt time.Time, likeCount, commentCount, favoriteCount uint32) float64 {
	if publishedAt.IsZero() {
		return 0
	}

	hoursSincePublish := time.Since(publishedAt.UTC()).Hours()
	publishedDays := int(hoursSincePublish/24) + 1
	if publishedDays < 1 {
		publishedDays = 1
	}

	score := float64(likeCount) + float64(commentCount)*3 + float64(favoriteCount)*2
	return score / float64(publishedDays)
}
