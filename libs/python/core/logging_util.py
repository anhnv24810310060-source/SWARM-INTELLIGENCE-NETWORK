import logging, os, json, sys, time, threading

class JsonLogFormatter(logging.Formatter):
    def format(self, record: logging.LogRecord) -> str:
        base = {
            "ts": time.strftime('%Y-%m-%dT%H:%M:%S', time.gmtime(record.created)),
            "level": record.levelname.lower(),
            "msg": record.getMessage(),
            "logger": record.name,
            "thread": record.threadName,
        }
        if record.exc_info:
            base["exc"] = self.formatException(record.exc_info)
        return json.dumps(base, ensure_ascii=False)

def init_logging(service: str):
    lvl = os.getenv("SWARM_LOG_LEVEL", "INFO").upper()
    json_mode = os.getenv("SWARM_JSON_LOG", "0").lower() in ("1", "true", "json")
    logging.root.handlers.clear()
    handler = logging.StreamHandler(sys.stdout)
    if json_mode:
        handler.setFormatter(JsonLogFormatter())
    else:
        handler.setFormatter(logging.Formatter(f"%(asctime)s %(levelname)s {service} %(message)s"))
    logging.root.addHandler(handler)
    logging.root.setLevel(lvl)
    logging.getLogger(__name__).info("logging initialized", extra={})
