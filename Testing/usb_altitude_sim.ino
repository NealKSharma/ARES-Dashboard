// ARES dashboard bench input. USB serial only, one integer altitude in feet per line.
// Opening USB serial starts one run. Closing and reopening starts another.
const unsigned long sampleMs = 50;
const long peakFeet = 1500;
bool running = false;
unsigned long startMs, lastSampleMs;

// Profile: 10 s on the pad, 20 s ascent to apogee, 60 s descent, then 0 ft.
const unsigned long padMs = 10000;
const unsigned long ascentMs = 20000;
const unsigned long descentMs = 60000;
const unsigned long landMs = padMs + ascentMs + descentMs;

long heightFeet(unsigned long ms) {
  if (ms < padMs || ms >= landMs) return 0;
  // Smooth synthetic ascent and descent, with zero slope at apogee.
  bool descending = ms >= padMs + ascentMs;
  float t = descending ? (ms - padMs - ascentMs) / (float)descentMs
                       : (ms - padMs) / (float)ascentMs;
  float fraction = t * t * (3.0f - 2.0f * t);
  return lroundf(peakFeet * (descending ? 1.0f - fraction : fraction));
}

void setup() { Serial.begin(115200); }
void loop() {
  if (!Serial) {
    running = false;
    return;
  }
  unsigned long now = millis();
  if (!running) {
    startMs = now;
    lastSampleMs = now - sampleMs;
    running = true;
  }
  if (now - lastSampleMs < sampleMs) return;
  lastSampleMs = now;
  unsigned long elapsed = now - startMs;
  Serial.println(heightFeet(elapsed));
  if (elapsed >= landMs) startMs = now - landMs; // Hold ground until the port closes.
}
