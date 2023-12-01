import { KVStore } from "fastly:kv-store";
import { Router } from "@fastly/expressly";
import { v4 as uuidv4 } from 'uuid';
import { env } from "fastly:env";

const router = new Router();
const types = {
  "image/jpeg":  "jpg",
  "image/gif":   "gif",
  "image/x-png": "png",
  "image/png": "png",
  "video/quicktime": "mov",
  "video/mp4": "mp4"
};

const mimes = {
  "jpg": "image/jpeg",
  "gif": "image/gif",
  "png": "image/png",
  "mov": "video/quicktime",
  "mp4": "video/mp4"
};

const magic = {
  "png": [
    [0, [0x89,0x50,0x4E,0x47]]
  ],
  "gif": [
    [0, [0x47,0x49,0x46,0x38,0x37,0x61]],
    [0, [0x47,0x49,0x46,0x38,0x39,0x61]],
  ],
  "jpg": [
    [0,[0xFF,0xD8,0xFF,0xDB]],
    [0,[0xFF,0xD8,0xFF,0xE0]],
    [0,[0xFF,0xD8,0xFF,0xEE]],
    [0,[0xFF,0xD8,0xFF,0xE1]]
  ],
  "jpeg": [
    [0,[0xFF,0xD8,0xFF,0xDB]],
    [0,[0xFF,0xD8,0xFF,0xE0]],
    [0,[0xFF,0xD8,0xFF,0xEE]],
    [0,[0xFF,0xD8,0xFF,0xE1]]
  ],
  "mp4": [
    [0,[0x66,0x74,0x79,0x70,0x69,0x73,0x6F,0x6D]],
    [0,[0x66,0x74,0x79,0x70,0x4D,0x53,0x4E,0x56]]
  ],
  "mov": [
    [4,[0x66,0x74,0x79,0x70,0x71,0x74,0x20,0x20]]
  ]
};

router.use((req, res) => {
  res.headers.set("service-version", env("FASTLY_SERVICE_VERSION"));
});

router.get(/([a-zA-Z0-9]+).(jpe?g|png|gif|mp4|mov)/, async (req, res) => {
  let id = req.path.substring(1, req.path.length - 4);
  let ext = req.path.substring(req.path.length - 3);
  let mime = mimes[ext];

  if (!mime) {
    res.withStatus(400).json({
      error: "unknown extension"
    });
    return;
  }

  let kv = new KVStore("images");
  const entry = await kv.get(id);
  res.headers.set("content-type", mime);
  res.send(entry.body);
});

router.options("(.*)", async (req, res) => {
  if (req.headers.has("Origin") && (req.headers.has("access-control-request-headers") || req.headers.has("access-control-request-method"))) {
    res.sendStatus(200);
    res.headers.set("access-control-allow-origin", req.headers.get('origin'));
    res.headers.set("access-control-allow-methods", "GET,HEAD,PUT,OPTIONS");
    res.headers.set("access-control-allow-headers", req.headers.get('access-control-request-headers') || '');
    res.headers.set("access-control-max-age", 86400);
  } else {
    res.sendStatus(400);
  }
});

router.put("/", async (req, res) => {
  let type = req.headers.get("content-type");
  let extension = types[type];

  if (!extension) {
    res.withStatus(400).json({
      error: "unknown content-type"
    });
    return;
  }

  let img = await req.arrayBuffer();

  if (magic[extension]) {
    let start = new Uint8Array(img.slice(0, 32));
    let ok = magic[extension].some((m) => {
      let offset = m[0];
      let bytes  = m[1];

      for (var i=0; i < bytes.length; i++) {
        if (bytes[i] != start[offset+i]) {
          return false
        }
      }

      return true
    });

    if (!ok) {
      res.withStatus(400).json({
        error: "invalid " + extension
      });
      return;
    }
  }

  let id = uuidv4().replaceAll("-", "");
  let kv = new KVStore("images");
  await kv.put(id, img)

  if (req.headers.has('origin')) {
    res.headers.set("access-control-allow-origin", req.headers.get('origin'));
  }

  res.json({
    status: "ok",
    data: {
      id: id,
      link: "https://"+req.headers.get("host")+"/"+id+"."+extension
    }
  });
});

router.listen();
