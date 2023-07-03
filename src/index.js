import { KVStore } from "fastly:kv-store";
import { Router } from "@fastly/expressly";
import { v4 as uuidv4 } from 'uuid';
import { env } from "fastly:env";

const router = new Router();
const types = {
  "image/jpeg":  "jpg",
  "image/gif":   "gif",
  "image/x-png": "png",
  "image/png": "png"
};
const mimes = {
  "jpg": "image/jpeg",
  "gif": "image/gif",
  "png": "image/png"
}

router.use((req, res) => {
  res.headers.set("service-version", env("FASTLY_SERVICE_VERSION"));
});

router.get(/([a-zA-Z0-9]+).(jpg|png|gif)/, async (req, res) => {
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

  let id = uuidv4().replaceAll("-", "");
  let kv = new KVStore("images");
  let img = await req.arrayBuffer();
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
