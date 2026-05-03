param(
    [string]$BaseUrl = "http://localhost:18080",
    [string]$ExistingAuthorUsername = "author001",
    [string]$ExistingAuthorPassword = "1234567",
    [string]$ExistingViewerUsername = "viewer001",
    [string]$ExistingViewerPassword = "123456",
    [string]$AuthorPrefix = "lt_author",
    [string]$ViewerPrefix = "lt_viewer",
    [string]$AuthorPassword = "1234567",
    [string]$ViewerPassword = "123456",
    [int]$AuthorCount = 4,
    [int]$ViewerCount = 20,
    [int]$VideosPerAuthor = 10,
    [int]$FollowPerViewer = 3,
    [int]$LikePerViewer = 10,
    [int]$FavoritePerViewer = 6,
    [int]$CommentPerViewer = 4,
    [string]$SummaryPath = "scripts/loadtest/seed-output.json"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.Net.Http

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = Split-Path -Parent (Split-Path -Parent $scriptRoot)
$videoSample = Get-ChildItem -Path (Join-Path $repoRoot "storage\videos") -Recurse -Filter *.mp4 | Select-Object -First 1
$coverSample = Get-ChildItem -Path (Join-Path $repoRoot "storage\covers") -Recurse -Filter *.png | Select-Object -First 1

if (-not $videoSample) {
    throw "No sample .mp4 file found under storage\videos"
}
if (-not $coverSample) {
    throw "No sample .png file found under storage\covers"
}

$summaryFullPath = if ([System.IO.Path]::IsPathRooted($SummaryPath)) {
    $SummaryPath
} else {
    Join-Path $repoRoot $SummaryPath
}
$summaryDirectory = Split-Path -Parent $summaryFullPath
if (-not (Test-Path $summaryDirectory)) {
    New-Item -ItemType Directory -Path $summaryDirectory | Out-Null
}

$client = New-Object System.Net.Http.HttpClient
$client.Timeout = [TimeSpan]::FromSeconds(120)

function Invoke-ApiJson {
    param(
        [string]$Method,
        [string]$Url,
        [object]$Body,
        [string]$Token = ""
    )

    $request = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::new($Method), $Url)
    if ($Token) {
        $request.Headers.Authorization = [System.Net.Http.Headers.AuthenticationHeaderValue]::new("Bearer", $Token)
    }
    if ($null -ne $Body) {
        $payload = $Body | ConvertTo-Json -Depth 10 -Compress
        $request.Content = [System.Net.Http.StringContent]::new($payload, [System.Text.Encoding]::UTF8, "application/json")
    }

    try {
        $response = $client.SendAsync($request).GetAwaiter().GetResult()
        $rawBody = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        if (-not $response.IsSuccessStatusCode) {
            throw "HTTP $Method $Url failed: status=$([int]$response.StatusCode) body=$rawBody"
        }

        $envelope = $rawBody | ConvertFrom-Json
        if ($envelope.code -ne 0) {
            throw "API $Method $Url failed: code=$($envelope.code) message=$($envelope.message)"
        }

        return $envelope.data
    }
    finally {
        $request.Dispose()
    }
}

function Invoke-ApiMultipart {
    param(
        [string]$Url,
        [hashtable]$Fields,
        [hashtable]$Files,
        [string]$Token
    )

    $request = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::Post, $Url)
    if ($Token) {
        $request.Headers.Authorization = [System.Net.Http.Headers.AuthenticationHeaderValue]::new("Bearer", $Token)
    }

    $multipart = [System.Net.Http.MultipartFormDataContent]::new()
    $streams = @()

    try {
        foreach ($fieldName in $Fields.Keys) {
            $multipart.Add([System.Net.Http.StringContent]::new([string]$Fields[$fieldName]), $fieldName)
        }

        foreach ($fileName in $Files.Keys) {
            $filePath = [string]$Files[$fileName]
            $stream = [System.IO.File]::OpenRead($filePath)
            $streams += $stream

            $content = [System.Net.Http.StreamContent]::new($stream)
            switch ([System.IO.Path]::GetExtension($filePath).ToLowerInvariant()) {
                ".mp4" { $content.Headers.ContentType = [System.Net.Http.Headers.MediaTypeHeaderValue]::new("video/mp4") }
                ".png" { $content.Headers.ContentType = [System.Net.Http.Headers.MediaTypeHeaderValue]::new("image/png") }
                ".jpg" { $content.Headers.ContentType = [System.Net.Http.Headers.MediaTypeHeaderValue]::new("image/jpeg") }
                ".jpeg" { $content.Headers.ContentType = [System.Net.Http.Headers.MediaTypeHeaderValue]::new("image/jpeg") }
                ".webp" { $content.Headers.ContentType = [System.Net.Http.Headers.MediaTypeHeaderValue]::new("image/webp") }
                default { $content.Headers.ContentType = [System.Net.Http.Headers.MediaTypeHeaderValue]::new("application/octet-stream") }
            }

            $multipart.Add($content, $fileName, [System.IO.Path]::GetFileName($filePath))
        }

        $request.Content = $multipart
        $response = $client.SendAsync($request).GetAwaiter().GetResult()
        $rawBody = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        if (-not $response.IsSuccessStatusCode) {
            throw "HTTP POST $Url failed: status=$([int]$response.StatusCode) body=$rawBody"
        }

        $envelope = $rawBody | ConvertFrom-Json
        if ($envelope.code -ne 0) {
            throw "API POST $Url failed: code=$($envelope.code) message=$($envelope.message)"
        }

        return $envelope.data
    }
    finally {
        foreach ($stream in $streams) {
            if ($stream) {
                $stream.Dispose()
            }
        }
        $multipart.Dispose()
        $request.Dispose()
    }
}

function Login-User {
    param(
        [string]$Username,
        [string]$Password
    )

    return Invoke-ApiJson -Method "POST" -Url "$BaseUrl/api/v1/auth/login" -Body @{
        username = $Username
        password = $Password
    }
}

function Register-User {
    param(
        [string]$Username,
        [string]$Password
    )

    return Invoke-ApiJson -Method "POST" -Url "$BaseUrl/api/v1/auth/register" -Body @{
        username = $Username
        password = $Password
    }
}

function Ensure-User {
    param(
        [string]$Username,
        [string]$Password
    )

    try {
        $login = Login-User -Username $Username -Password $Password
    }
    catch {
        Register-User -Username $Username -Password $Password | Out-Null
        $login = Login-User -Username $Username -Password $Password
    }

    $me = Invoke-ApiJson -Method "GET" -Url "$BaseUrl/api/v1/auth/me" -Body $null -Token $login.access_token
    return [PSCustomObject]@{
        username = $Username
        password = $Password
        token    = $login.access_token
        id       = [int]$me.id
    }
}

function Publish-Video {
    param(
        [string]$Token,
        [string]$Title
    )

    return Invoke-ApiMultipart -Url "$BaseUrl/api/v1/videos" -Fields @{
        title = $Title
    } -Files @{
        video = $videoSample.FullName
        cover = $coverSample.FullName
    } -Token $Token
}

function Invoke-EmptyPost {
    param(
        [string]$Url,
        [string]$Token
    )

    return Invoke-ApiJson -Method "POST" -Url $Url -Body $null -Token $Token
}

function Get-RandomSubset {
    param(
        [object[]]$Items,
        [int]$Count
    )

    if (-not $Items -or $Items.Count -eq 0 -or $Count -le 0) {
        return @()
    }

    $target = [Math]::Min($Count, $Items.Count)
    return $Items | Sort-Object { Get-Random } | Select-Object -First $target
}

Write-Host "Using sample video: $($videoSample.FullName)"
Write-Host "Using sample cover: $($coverSample.FullName)"

$authors = @()
$viewers = @()
$allVideoIds = New-Object System.Collections.Generic.List[int]
$videosByAuthor = @{}

$existingAuthor = Ensure-User -Username $ExistingAuthorUsername -Password $ExistingAuthorPassword
$authors += $existingAuthor

$existingViewer = Ensure-User -Username $ExistingViewerUsername -Password $ExistingViewerPassword
$viewers += $existingViewer

for ($index = 1; $index -le $AuthorCount; $index++) {
    $username = "{0}_{1:d3}" -f $AuthorPrefix, $index
    $authors += Ensure-User -Username $username -Password $AuthorPassword
}

for ($index = 1; $index -le $ViewerCount; $index++) {
    $username = "{0}_{1:d3}" -f $ViewerPrefix, $index
    $viewers += Ensure-User -Username $username -Password $ViewerPassword
}

Write-Host "Prepared $($authors.Count) authors and $($viewers.Count) viewers"

foreach ($author in $authors) {
    $authorVideoIds = New-Object System.Collections.Generic.List[int]

    for ($videoIndex = 1; $videoIndex -le $VideosPerAuthor; $videoIndex++) {
        $title = "seed-$($author.username)-$videoIndex"
        $video = Publish-Video -Token $author.token -Title $title
        $videoId = [int]$video.id
        $authorVideoIds.Add($videoId)
        $allVideoIds.Add($videoId)
        Write-Host "Published video $videoId for $($author.username)"
    }

    $videosByAuthor[[string]$author.id] = @($authorVideoIds)
}

foreach ($viewer in $viewers) {
    $followAuthors = Get-RandomSubset -Items ($authors | Where-Object { $_.id -ne $viewer.id }) -Count $FollowPerViewer
    $followAuthorIds = New-Object System.Collections.Generic.List[int]

    foreach ($author in $followAuthors) {
        Invoke-EmptyPost -Url "$BaseUrl/api/v1/users/$($author.id)/follow" -Token $viewer.token | Out-Null
        $followAuthorIds.Add([int]$author.id)
    }

    $candidateVideoIds = New-Object System.Collections.Generic.List[int]
    foreach ($authorId in $followAuthorIds) {
        foreach ($videoId in $videosByAuthor[[string]$authorId]) {
            $candidateVideoIds.Add([int]$videoId)
        }
    }
    if ($candidateVideoIds.Count -eq 0) {
        foreach ($videoId in $allVideoIds) {
            $candidateVideoIds.Add([int]$videoId)
        }
    }

    $candidateVideoArray = @($candidateVideoIds)
    foreach ($videoId in (Get-RandomSubset -Items $candidateVideoArray -Count $LikePerViewer)) {
        Invoke-EmptyPost -Url "$BaseUrl/api/v1/videos/$videoId/likes" -Token $viewer.token | Out-Null
    }
    foreach ($videoId in (Get-RandomSubset -Items $candidateVideoArray -Count $FavoritePerViewer)) {
        Invoke-EmptyPost -Url "$BaseUrl/api/v1/videos/$videoId/favorites" -Token $viewer.token | Out-Null
    }
    foreach ($videoId in (Get-RandomSubset -Items $candidateVideoArray -Count $CommentPerViewer)) {
        Invoke-ApiJson -Method "POST" -Url "$BaseUrl/api/v1/videos/$videoId/comments" -Body @{
            content = "seed-comment-$($viewer.username)-$videoId-$(Get-Random -Minimum 1000 -Maximum 9999)"
        } -Token $viewer.token | Out-Null
    }

    $viewer | Add-Member -NotePropertyName follow_author_ids -NotePropertyValue @($followAuthorIds) -Force
}

$summary = [PSCustomObject]@{
    generated_at = [DateTime]::UtcNow.ToString("o")
    base_url     = $BaseUrl
    sample_files = [PSCustomObject]@{
        video_path = $videoSample.FullName
        cover_path = $coverSample.FullName
    }
    authors      = @(
        foreach ($author in $authors) {
            [PSCustomObject]@{
                id        = [int]$author.id
                username  = [string]$author.username
                password  = [string]$author.password
                video_ids = @($videosByAuthor[[string]$author.id])
            }
        }
    )
    viewers      = @(
        foreach ($viewer in $viewers) {
            [PSCustomObject]@{
                id                = [int]$viewer.id
                username          = [string]$viewer.username
                password          = [string]$viewer.password
                follow_author_ids = @($viewer.follow_author_ids)
            }
        }
    )
    video_ids    = @($allVideoIds)
}

$summaryJson = $summary | ConvertTo-Json -Depth 10
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText($summaryFullPath, $summaryJson, $utf8NoBom)
Write-Host "Seed summary written to $summaryFullPath"
Write-Host "Total authors: $($summary.authors.Count)"
Write-Host "Total viewers: $($summary.viewers.Count)"
Write-Host "Total videos: $($summary.video_ids.Count)"
