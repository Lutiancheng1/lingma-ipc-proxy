export namespace main {

	export class ModelInfo {
	    id: string;
	    name: string;

	    static createFrom(source: any = {}) {
	        return new ModelInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	    }
	}
	export class ProxyStatus {
	    running: boolean;
	    addr: string;
	    models: number;
	    model?: string;
	    startedAt?: string;

	    static createFrom(source: any = {}) {
	        return new ProxyStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.addr = source["addr"];
	        this.models = source["models"];
	        this.model = source["model"];
	        this.startedAt = source["startedAt"];
	    }
	}
	export class RequestRecord {
	    time: string;
	    method: string;
	    path: string;
	    statusCode: number;
	    duration: string;
	    reqBody?: string;
	    respBody?: string;

	    static createFrom(source: any = {}) {
	        return new RequestRecord(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.method = source["method"];
	        this.path = source["path"];
	        this.statusCode = source["statusCode"];
	        this.duration = source["duration"];
	        this.reqBody = source["reqBody"];
	        this.respBody = source["respBody"];
	    }
	}

}

export namespace service {

	export class Config {
	    Host: string;
	    Port: number;
	    Backend: string;
	    Transport: string;
	    Pipe: string;
	    WebSocketURL: string;
	    RemoteBaseURL: string;
	    RemoteAuthFile: string;
	    RemoteVersion: string;
	    Cwd: string;
	    CurrentFilePath: string;
	    Mode: string;
	    Model: string;
	    ShellType: string;
	    SessionMode: string;
	    Timeout: number;

	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Host = source["Host"];
	        this.Port = source["Port"];
	        this.Backend = source["Backend"];
	        this.Transport = source["Transport"];
	        this.Pipe = source["Pipe"];
	        this.WebSocketURL = source["WebSocketURL"];
	        this.RemoteBaseURL = source["RemoteBaseURL"];
	        this.RemoteAuthFile = source["RemoteAuthFile"];
	        this.RemoteVersion = source["RemoteVersion"];
	        this.Cwd = source["Cwd"];
	        this.CurrentFilePath = source["CurrentFilePath"];
	        this.Mode = source["Mode"];
	        this.Model = source["Model"];
	        this.ShellType = source["ShellType"];
	        this.SessionMode = source["SessionMode"];
	        this.Timeout = source["Timeout"];
	    }
	}

}
