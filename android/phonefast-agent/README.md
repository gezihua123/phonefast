# phonefast Android Agent

This is a minimal patch for scrcpy-server to add a fast UI hierarchy dump socket.

## Integration Steps

1. Copy `UISocketHandler.java` into the scrcpy source:
   ```
   cp UISocketHandler.java ~/Desktop/code/scrcpy/server/src/main/java/com/genymobile/scrcpy/control/
   ```

2. Apply the patch to `Server.java` (see `server.patch`):
   - Add UISocketHandler field and initialization
   - Start UI socket before entering the main loop
   - Pass UiAutomation instance to UISocketHandler

3. Rebuild scrcpy-server.jar:
   ```
   cd ~/Desktop/code/scrcpy
   ./gradlew :server:jar  # or assembleRelease
   ```

4. Copy the built jar to phonefast:
   ```
   cp server/build/libs/scrcpy-server.jar ~/Desktop/phonefast/android/scrcpy-server.jar
   ```

## Protocol

- Socket name: `scrcpy_<scid>_ui` (e.g., `scrcpy_0000003f_ui`)
- Request: `dump\0` (5 bytes)
- Response: 4-byte big-endian length + UTF-8 JSON

## JSON Format

```json
{
  "elements": [
    {
      "index": 0,
      "text": "Settings",
      "content_desc": "",
      "resource_id": "com.example:id/btn_settings",
      "class_name": "android.widget.Button",
      "bounds": [16, 48, 200, 144],
      "center": [108, 96],
      "clickable": true,
      "enabled": true,
      "focused": false,
      "selected": false
    }
  ]
}
```
