export namespace main {

	export class DetectionInfo {
	    listenUrl: string;
	    backend: string;
	    backendLabel: string;
	    ipcSuccess: boolean;
	    ipcTransport?: string;
	    ipcEndpoint?: string;
	    ipcError?: string;
	    remoteBaseUrl: string;
	    remoteBaseUrlSource?: string;
	    remoteCredentialSuccess: boolean;
	    remoteCredentialSource?: string;
	    remoteUserId?: string;
	    remoteMachineId?: string;
	    remoteTokenExpireAt?: string;
	    remoteTokenExpired: boolean;
	    remoteCredentialError?: string;

	    static createFrom(source: any = {}) {
	        return new DetectionInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.listenUrl = source["listenUrl"];
	        this.backend = source["backend"];
	        this.backendLabel = source["backendLabel"];
	        this.ipcSuccess = source["ipcSuccess"];
	        this.ipcTransport = source["ipcTransport"];
	        this.ipcEndpoint = source["ipcEndpoint"];
	        this.ipcError = source["ipcError"];
	        this.remoteBaseUrl = source["remoteBaseUrl"];
	        this.remoteBaseUrlSource = source["remoteBaseUrlSource"];
	        this.remoteCredentialSuccess = source["remoteCredentialSuccess"];
	        this.remoteCredentialSource = source["remoteCredentialSource"];
	        this.remoteUserId = source["remoteUserId"];
	        this.remoteMachineId = source["remoteMachineId"];
	        this.remoteTokenExpireAt = source["remoteTokenExpireAt"];
	        this.remoteTokenExpired = source["remoteTokenExpired"];
	        this.remoteCredentialError = source["remoteCredentialError"];
	    }
	}
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
	    backend: string;
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
	        this.backend = source["backend"];
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
