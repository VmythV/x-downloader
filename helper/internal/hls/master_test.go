package hls

import "testing"

const sampleMaster = `#EXTM3U
#EXT-X-MEDIA:NAME="Audio",TYPE=AUDIO,GROUP-ID="audio-128000",AUTOSELECT=YES,URI="/amplify_video/2075181378543779840/pl/mp4a/128000/audio.m3u8"
#EXT-X-MEDIA:NAME="Audio",TYPE=AUDIO,GROUP-ID="audio-64000",AUTOSELECT=YES,URI="/amplify_video/2075181378543779840/pl/mp4a/64000/audio.m3u8"
#EXT-X-STREAM-INF:AVERAGE-BANDWIDTH=1264598,BANDWIDTH=2095007,RESOLUTION=1280x720,CODECS="mp4a.40.2,avc1.64001F",AUDIO="audio-128000"
/amplify_video/2075181378543779840/pl/avc1/1280x720/video.m3u8
#EXT-X-STREAM-INF:AVERAGE-BANDWIDTH=416079,BANDWIDTH=664141,RESOLUTION=640x360,CODECS="mp4a.40.2,avc1.4D401E",AUDIO="audio-64000"
/amplify_video/2075181378543779840/pl/avc1/640x360/video.m3u8
`

func TestParseMasterSortsQualityAndAssociatesAudioGroup(t *testing.T) {
	master, err := ParseMaster(sampleMaster, "https://video.twimg.com/amplify_video/2075181378543779840/pl/master.m3u8?tag=14")
	if err != nil {
		t.Fatal(err)
	}
	if master.MediaID != "2075181378543779840" || len(master.Variants) != 2 {
		t.Fatalf("unexpected master: %+v", master)
	}
	best := master.Variants[0]
	if best.Height != 720 || best.Audio == nil || best.Audio.Bitrate != 128000 {
		t.Fatalf("unexpected best variant: %+v", best)
	}
	if best.Codecs != "mp4a.40.2,avc1.64001F" {
		t.Fatalf("quoted codec list was parsed incorrectly: %s", best.Codecs)
	}
}

func TestRejectsCrossOriginChildPlaylist(t *testing.T) {
	text := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1,RESOLUTION=1x1\nhttps://example.com/video.m3u8\n"
	if _, err := ParseMaster(text, "https://video.twimg.com/amplify_video/1/pl/master.m3u8"); err == nil {
		t.Fatal("expected a cross-origin child playlist to be rejected")
	}
}
