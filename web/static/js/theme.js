(function () {
	var t = localStorage.getItem("angrybox-theme") || "sand";
	var legacy = {
		tokyonight: "graphite",
		"tokyonight-day": "sand",
		"tokyonight-storm": "graphite",
		dark: "graphite",
		light: "sand",
	};
	if (legacy[t]) t = legacy[t];
	var valid = ["sand", "slate", "graphite", "night"];
	if (valid.indexOf(t) < 0) t = "sand";
	document.documentElement.setAttribute("data-theme", t);
})();
